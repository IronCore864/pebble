// Copyright (c) 2024 Canonical Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License version 3 as
// published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package checkstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	tombpkg "gopkg.in/tomb.v2"

	"github.com/canonical/pebble/internals/logger"
	"github.com/canonical/pebble/internals/overlord/state"
	"github.com/canonical/pebble/internals/plan"
)

type checkContext struct {
	task       *state.Task
	tomb       *tombpkg.Tomb
	chk        checker
	changeID   string
	config     *plan.Check
	details    *checkDetails
	sendResult bool
	result     chan error
}

// performCheckAndSendResult runs the check and optionally sends the result.
func (m *CheckManager) performCheckAndSendResult(ctx *checkContext) error {
	err := runCheck(ctx.tomb.Context(nil), ctx.chk, ctx.config.Timeout.Value)
	if ctx.sendResult {
		ctx.result <- err
	}
	if !ctx.tomb.Alive() {
		return checkStopped(ctx.config.Name, ctx.task.Kind(), ctx.tomb.Err())
	}
	if err != nil {
		// Record check failure and perform any action if the threshold
		// is reached (for example, restarting a service).
		m.incFailureCount(ctx.config)
		ctx.details.Failures++
		atThreshold := ctx.details.Failures >= ctx.config.Threshold
		if !atThreshold {
			// Update number of failures in check info. In threshold
			// case, check data will be updated with new change ID by
			// changeStatusChanged.
			m.updateCheckData(ctx.config, ctx.changeID, ctx.details.Failures)
		}

		m.state.Lock()
		if atThreshold {
			ctx.details.Proceed = true
		} else {
			// Add error to task log, but only if we haven't reached the
			// threshold. When we hit the threshold, the "return err"
			// below will cause the error to be logged.
			logTaskError(ctx.task, err)
		}
		ctx.task.Set(checkDetailsAttr, &ctx.details)
		m.state.Unlock()

		logger.Noticef("Check %q failure %d/%d: %v", ctx.config.Name, ctx.details.Failures, ctx.config.Threshold, err)
		if atThreshold {
			logger.Noticef("Check %q threshold %d hit, triggering action and recovering", ctx.config.Name, ctx.config.Threshold)
			m.callFailureHandlers(ctx.config.Name)
			// Returning the error means perform-check goes to Error status
			// and logs the error to the task log.
			return err
		}
	} else {
		m.incSuccessCount(ctx.config)
		if ctx.details.Failures > 0 {
			m.updateCheckData(ctx.config, ctx.changeID, 0)

			m.state.Lock()
			ctx.task.Logf("succeeded after %s", pluralise(ctx.details.Failures, "failure", "failures"))
			ctx.details.Failures = 0
			ctx.task.Set(checkDetailsAttr, &ctx.details)
			m.state.Unlock()
		}
	}
	return nil
}

func (m *CheckManager) doPerformCheck(task *state.Task, tomb *tombpkg.Tomb) error {
	m.state.Lock()
	changeID := task.Change().ID()
	var details checkDetails
	err := task.Get(checkDetailsAttr, &details)
	config := m.state.Cached(performConfigKey{changeID}).(*plan.Check) // panic if key not present (always should be)
	m.state.Unlock()
	if err != nil {
		return fmt.Errorf("cannot get check details for perform-check task %q: %v", task.ID(), err)
	}

	logger.Debugf("Performing check %q with period %v", details.Name, config.Period.Value)

	// Retrieve CheckInfo and its channels
	m.checksLock.Lock()
	m.ensureCheck(config.Name)
	checkInfo := m.checks[details.Name]
	refresh := checkInfo.refresh
	result := checkInfo.result
	m.checksLock.Unlock()

	ticker := time.NewTicker(config.Period.Value)
	defer ticker.Stop()

	chk := newChecker(config)

	ctx := &checkContext{
		task:       task,
		tomb:       tomb,
		chk:        chk,
		changeID:   changeID,
		config:     config,
		details:    &details,
		sendResult: false,
		result:     result,
	}

	for {
		select {
		case <-refresh:
			// Reset ticker on refresh.
			ticker.Reset(config.Period.Value)
			ctx.sendResult = true
			err := m.performCheckAndSendResult(ctx)
			if err != nil {
				return err
			}
		case <-ticker.C:
			err := m.performCheckAndSendResult(ctx)
			if err != nil {
				return err
			}
		case <-tomb.Dying():
			return checkStopped(config.Name, task.Kind(), tomb.Err())
		}
	}
}

func runCheck(ctx context.Context, chk checker, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := chk.check(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("check timed out after %v", timeout)
	}
	return err
}

// recoverCheckAndSendResult runs the check and optionally sends the result.
func (m *CheckManager) recoverCheckAndSendResult(ctx *checkContext) error {
	err := runCheck(ctx.tomb.Context(nil), ctx.chk, ctx.config.Timeout.Value)
	if ctx.sendResult {
		ctx.result <- err
	}
	if !ctx.tomb.Alive() {
		return checkStopped(ctx.config.Name, ctx.task.Kind(), ctx.tomb.Err())
	}
	if err != nil {
		m.incFailureCount(ctx.config)
		ctx.details.Failures++
		m.updateCheckData(ctx.config, ctx.changeID, ctx.details.Failures)

		m.state.Lock()
		ctx.task.Set(checkDetailsAttr, &ctx.details)
		logTaskError(ctx.task, err)
		m.state.Unlock()

		logger.Noticef("Check %q failure %d/%d: %v", ctx.config.Name, ctx.details.Failures, ctx.config.Threshold, err)

	} else {
		// Check succeeded, switch to performing a succeeding check.
		// Check info will be updated with new change ID by changeStatusChanged.
		m.incSuccessCount(ctx.config)
		ctx.details.Failures = 0 // not strictly needed, but just to be safe
		ctx.details.Proceed = true
		m.state.Lock()
		ctx.task.Set(checkDetailsAttr, &ctx.details)
		m.state.Unlock()
	}
	return err
}

func (m *CheckManager) doRecoverCheck(task *state.Task, tomb *tombpkg.Tomb) error {
	m.state.Lock()
	changeID := task.Change().ID()
	var details checkDetails
	err := task.Get(checkDetailsAttr, &details)
	config := m.state.Cached(recoverConfigKey{changeID}).(*plan.Check) // panic if key not present (always should be)
	m.state.Unlock()
	if err != nil {
		return fmt.Errorf("cannot get check details for recover-check task %q: %v", task.ID(), err)
	}

	logger.Debugf("Recovering check %q with period %v", details.Name, config.Period.Value)

	// Retrieve CheckInfo and its channels
	m.checksLock.Lock()
	m.ensureCheck(config.Name)
	checkInfo := m.checks[details.Name]
	refresh := checkInfo.refresh
	result := checkInfo.result
	m.checksLock.Unlock()

	ticker := time.NewTicker(config.Period.Value)
	defer ticker.Stop()

	chk := newChecker(config)

	ctx := &checkContext{
		task:       task,
		tomb:       tomb,
		chk:        chk,
		changeID:   changeID,
		config:     config,
		details:    &details,
		sendResult: false,
		result:     result,
	}

	for {
		select {
		case <-refresh:
			// Reset ticker on refresh.
			ticker.Reset(config.Period.Value)
			ctx.sendResult = true
			err := m.recoverCheckAndSendResult(ctx)
			if err != nil {
				if err == tomb.Err() {
					return err
				}
				break
			}
			return err
		case <-ticker.C:
			err := m.recoverCheckAndSendResult(ctx)
			if err != nil {
				if err == tomb.Err() {
					return err
				}
				break
			}
			return err
		case <-tomb.Dying():
			return checkStopped(config.Name, task.Kind(), tomb.Err())
		}
	}
}

func logTaskError(task *state.Task, err error) {
	message := err.Error()
	var detailsErr *detailsError
	if errors.As(err, &detailsErr) && detailsErr.Details() != "" {
		message += "; " + detailsErr.Details()
	}
	task.Errorf("%s", message)
}

func checkStopped(checkName, taskKind string, tombErr error) error {
	reason := " (no error)"
	if tombErr != nil {
		reason = ": " + tombErr.Error()
	}
	logger.Debugf("Check %q stopped during %s%s", checkName, taskKind, reason)
	return tombErr
}

func pluralise(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func (m *CheckManager) RunCheck(ctx context.Context, check *plan.Check) error {
	// chk := newChecker(check)
	// return runCheck(ctx, chk, check.Timeout.Value)
	m.checksLock.Lock()
	checkData := m.ensureCheck(check.Name)
	refresh := checkData.refresh
	result := checkData.result
	m.checksLock.Unlock()

	if refresh == nil || result == nil {
		return fmt.Errorf("refresh channels not initialized for check %q", checkData.name)
	}

	refresh <- struct{}{}
	select {
	case result := <-result:
		return result
	case <-ctx.Done():
		return ctx.Err()
	}
}
