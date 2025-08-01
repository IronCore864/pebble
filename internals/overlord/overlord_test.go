// Copyright (c) 2014-2020 Canonical Ltd
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

package overlord_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/tomb.v2"

	"github.com/canonical/pebble/cmd"
	"github.com/canonical/pebble/internals/osutil"
	"github.com/canonical/pebble/internals/overlord"
	"github.com/canonical/pebble/internals/overlord/patch"
	"github.com/canonical/pebble/internals/overlord/restart"
	"github.com/canonical/pebble/internals/overlord/state"
	"github.com/canonical/pebble/internals/testutil"
)

func TestOverlord(t *testing.T) { TestingT(t) }

type overlordSuite struct {
	dir       string
	statePath string
}

var _ = Suite(&overlordSuite{})

type ticker struct {
	tickerChannel chan time.Time
}

func (w *ticker) tick(n int) {
	for i := 0; i < n; i++ {
		w.tickerChannel <- time.Now()
	}
}

func fakePruneTicker() (w *ticker, restore func()) {
	w = &ticker{
		tickerChannel: make(chan time.Time),
	}
	restore = overlord.FakePruneTicker(func(t *time.Ticker) <-chan time.Time {
		return w.tickerChannel
	})
	return w, restore
}

func (ovs *overlordSuite) SetUpTest(c *C) {
	ovs.dir = c.MkDir()
	ovs.statePath = filepath.Join(ovs.dir, cmd.StateFile)
}

func (ovs *overlordSuite) TearDownTest(c *C) {
}

func (ovs *overlordSuite) TestNew(c *C) {
	restore := patch.Fake(42, 2, nil)
	defer restore()

	o, _ := overlord.New(&overlord.Options{PebbleDir: ovs.dir})

	c.Check(o.StateEngine(), NotNil)
	c.Check(o.TaskRunner(), NotNil)
	c.Check(o.RestartManager(), NotNil)

	s := o.State()
	c.Check(s, NotNil)
	c.Check(o.Engine().State(), Equals, s)

	s.Lock()
	defer s.Unlock()
	var patchLevel, patchSublevel int
	s.Get("patch-level", &patchLevel)
	c.Check(patchLevel, Equals, 42)
	s.Get("patch-sublevel", &patchSublevel)
	c.Check(patchSublevel, Equals, 2)

	_, err := os.Stat(ovs.statePath)
	c.Assert(err, IsNil)
}

func (ovs *overlordSuite) TestNewInMemoryBackend(c *C) {
	restore := patch.Fake(42, 2, nil)
	defer restore()

	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir, Persist: overlord.PersistNever})
	c.Assert(err, IsNil)
	c.Check(o, NotNil)

	c.Check(o.StateEngine(), NotNil)
	c.Check(o.TaskRunner(), NotNil)
	c.Check(o.RestartManager(), NotNil)

	s := o.State()
	c.Check(s, NotNil)
	c.Check(o.Engine().State(), Equals, s)

	s.Lock()
	defer s.Unlock()
	var patchLevel, patchSublevel int
	s.Get("patch-level", &patchLevel)
	c.Check(patchLevel, Equals, 42)
	s.Get("patch-sublevel", &patchSublevel)
	c.Check(patchSublevel, Equals, 2)

	_, err = os.Stat(ovs.statePath)
	c.Check(errors.Is(err, fs.ErrNotExist), Equals, true)
}

func (ovs *overlordSuite) TestNewWithGoodState(c *C) {
	fakeState := []byte(fmt.Sprintf(`{
		"data": {"patch-level": %d, "patch-sublevel": %d, "patch-sublevel-last-version": %q, "some": "data"},
		"changes": null,
		"tasks": null,
		"last-change-id": 0,
		"last-task-id": 0,
		"last-lane-id": 0,
		"last-notice-id": 0
	}`, patch.Level, patch.Sublevel, cmd.Version))
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)
	c.Check(o.RestartManager(), NotNil)

	state := o.State()
	c.Assert(err, IsNil)
	state.Lock()
	defer state.Unlock()

	d, err := state.MarshalJSON()
	c.Assert(err, IsNil)

	var got, expected map[string]any
	err = json.Unmarshal(d, &got)
	c.Assert(err, IsNil)
	err = json.Unmarshal(fakeState, &expected)
	c.Assert(err, IsNil)

	data, _ := got["data"].(map[string]any)
	c.Assert(data, NotNil)

	c.Check(got, DeepEquals, expected)
}

func (ovs *overlordSuite) TestNewWithInvalidState(c *C) {
	fakeState := []byte(``)
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, ErrorMatches, "cannot read state: EOF")
}

func (ovs *overlordSuite) TestNewWithPatches(c *C) {
	p := func(s *state.State) error {
		s.Set("patched", true)
		return nil
	}
	sp := func(s *state.State) error {
		s.Set("patched2", true)
		return nil
	}
	patch.Fake(1, 1, map[int][]patch.PatchFunc{1: {p, sp}})

	fakeState := []byte(`{"data":{"patch-level":0, "patch-sublevel":0}}`)
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	state := o.State()
	c.Assert(err, IsNil)
	state.Lock()
	defer state.Unlock()

	var level int
	err = state.Get("patch-level", &level)
	c.Assert(err, IsNil)
	c.Check(level, Equals, 1)

	var sublevel int
	c.Assert(state.Get("patch-sublevel", &sublevel), IsNil)
	c.Check(sublevel, Equals, 1)

	var b bool
	err = state.Get("patched", &b)
	c.Assert(err, IsNil)
	c.Check(b, Equals, true)

	c.Assert(state.Get("patched2", &b), IsNil)
	c.Check(b, Equals, true)
}

type witnessManager struct {
	state          *state.State
	expectedEnsure int
	ensureCalled   chan struct{}
	ensureCallback func(s *state.State) error
	startedUp      int
}

func (wm *witnessManager) StartUp() error {
	wm.startedUp++
	return nil
}

func (wm *witnessManager) Ensure() error {
	if wm.expectedEnsure--; wm.expectedEnsure == 0 {
		close(wm.ensureCalled)
		return nil
	}
	if wm.ensureCallback != nil {
		return wm.ensureCallback(wm.state)
	}
	return nil
}

func (ovs *overlordSuite) TestTrivialRunAndStop(c *C) {
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	err = o.StartUp()
	c.Assert(err, IsNil)

	o.Loop()

	err = o.Stop()
	c.Assert(err, IsNil)
}

func (ovs *overlordSuite) TestUnknownTasks(c *C) {
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	// unknown tasks are ignored and succeed
	st := o.State()
	st.Lock()
	defer st.Unlock()
	t := st.NewTask("unknown", "...")
	chg := st.NewChange("change-w-unknown", "...")
	chg.AddTask(t)

	st.Unlock()
	err = o.Settle(1 * time.Second)
	st.Lock()
	c.Assert(err, IsNil)

	c.Check(chg.Status(), Equals, state.DoneStatus)
}

func (ovs *overlordSuite) TestEnsureLoopRunAndStop(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Millisecond)
	defer restoreIntv()
	o := overlord.Fake()

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 3,
		ensureCalled:   make(chan struct{}),
	}
	o.AddManager(witness)

	err := o.StartUp()
	c.Assert(err, IsNil)

	o.Loop()
	defer o.Stop()

	t0 := time.Now()
	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
	c.Check(time.Since(t0) >= 10*time.Millisecond, Equals, true)

	err = o.Stop()
	c.Assert(err, IsNil)

	c.Check(witness.startedUp, Equals, 1)
}

func (ovs *overlordSuite) TestEnsureLoopMediatedEnsureBeforeImmediate(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	ensure := func(s *state.State) error {
		s.EnsureBefore(0)
		return nil
	}

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 2,
		ensureCalled:   make(chan struct{}),
		ensureCallback: ensure,
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	o.Loop()
	defer o.Stop()

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
}

func (ovs *overlordSuite) TestEnsureLoopMediatedEnsureBefore(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	ensure := func(s *state.State) error {
		s.EnsureBefore(10 * time.Millisecond)
		return nil
	}

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 2,
		ensureCalled:   make(chan struct{}),
		ensureCallback: ensure,
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	o.Loop()
	defer o.Stop()

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
}

func (ovs *overlordSuite) TestEnsureBeforeSleepy(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	ensure := func(s *state.State) error {
		overlord.FakeEnsureNext(o, time.Now().Add(-10*time.Hour))
		s.EnsureBefore(0)
		return nil
	}

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 2,
		ensureCalled:   make(chan struct{}),
		ensureCallback: ensure,
	}
	o.AddManager(witness)

	err := o.StartUp()
	c.Assert(err, IsNil)

	o.Loop()
	defer o.Stop()

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
}

func (ovs *overlordSuite) TestEnsureBeforeLater(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	ensure := func(s *state.State) error {
		overlord.FakeEnsureNext(o, time.Now().Add(-10*time.Hour))
		s.EnsureBefore(time.Second * 5)
		return nil
	}

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 2,
		ensureCalled:   make(chan struct{}),
		ensureCallback: ensure,
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	o.Loop()
	defer o.Stop()

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
}

func (ovs *overlordSuite) TestEnsureLoopMediatedEnsureBeforeOutsideEnsure(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	ch := make(chan struct{})
	ensure := func(s *state.State) error {
		close(ch)
		return nil
	}

	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 2,
		ensureCalled:   make(chan struct{}),
		ensureCallback: ensure,
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	o.Loop()
	defer o.Stop()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}

	o.State().EnsureBefore(0)

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}
}

func (ovs *overlordSuite) TestEnsureLoopPrune(c *C) {
	restoreIntv := overlord.FakePruneInterval(200*time.Millisecond, 1000*time.Millisecond, 1000*time.Millisecond)
	defer restoreIntv()
	o := overlord.Fake()

	st := o.State()
	st.Lock()
	t1 := st.NewTask("foo", "...")
	chg1 := st.NewChange("abort", "...")
	chg1.AddTask(t1)
	chg2 := st.NewChange("prune", "...")
	chg2.SetStatus(state.DoneStatus)
	t0 := chg2.ReadyTime()
	st.Unlock()

	// observe the loop cycles to detect when prune should have happened
	pruneHappened := make(chan struct{})
	cycles := -1
	waitForPrune := func(_ *state.State) error {
		if cycles == -1 {
			if time.Since(t0) > 1000*time.Millisecond {
				cycles = 2 // wait a couple more loop cycles
			}
			return nil
		}
		if cycles > 0 {
			cycles--
			if cycles == 0 {
				close(pruneHappened)
			}
		}
		return nil
	}
	witness := &witnessManager{
		ensureCallback: waitForPrune,
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	o.Loop()

	select {
	case <-pruneHappened:
	case <-time.After(2 * time.Second):
		c.Fatal("Pruning should have happened by now")
	}

	err := o.Stop()
	c.Assert(err, IsNil)

	st.Lock()
	defer st.Unlock()

	c.Assert(st.Change(chg1.ID()), Equals, chg1)
	c.Assert(st.Change(chg2.ID()), IsNil)

	c.Assert(t1.Status(), Equals, state.HoldStatus)
}

func (ovs *overlordSuite) TestEnsureLoopPruneRunsMultipleTimes(c *C) {
	restoreIntv := overlord.FakePruneInterval(100*time.Millisecond, 1000*time.Millisecond, 1*time.Hour)
	defer restoreIntv()
	o := overlord.Fake()

	// create two changes, one that can be pruned now, one in progress
	st := o.State()
	st.Lock()
	t1 := st.NewTask("foo", "...")
	chg1 := st.NewChange("pruneNow", "...")
	chg1.AddTask(t1)
	t1.SetStatus(state.DoneStatus)
	t2 := st.NewTask("foo", "...")
	chg2 := st.NewChange("pruneNext", "...")
	chg2.AddTask(t2)
	t2.SetStatus(state.DoStatus)
	c.Check(st.Changes(), HasLen, 2)
	st.Unlock()

	// start the loop that runs the prune ticker
	o.Loop()

	// ensure the first change is pruned
	time.Sleep(1500 * time.Millisecond)
	st.Lock()
	c.Check(st.Changes(), HasLen, 1)
	st.Unlock()

	// ensure the second is also purged after it is ready
	st.Lock()
	chg2.SetStatus(state.DoneStatus)
	st.Unlock()
	time.Sleep(1500 * time.Millisecond)
	st.Lock()
	c.Check(st.Changes(), HasLen, 0)
	st.Unlock()

	// cleanup loop ticker
	err := o.Stop()
	c.Assert(err, IsNil)
}

func (ovs *overlordSuite) TestOverlordStartUpSetsStartOfOperation(c *C) {
	restoreIntv := overlord.FakePruneInterval(100*time.Millisecond, 1000*time.Millisecond, 1*time.Hour)
	defer restoreIntv()

	// use real overlord, we need device manager to be there
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	st := o.State()
	st.Lock()
	defer st.Unlock()

	// validity check, not set
	var opTime time.Time
	c.Assert(st.Get("start-of-operation-time", &opTime), testutil.ErrorIs, state.ErrNoState)
	st.Unlock()

	c.Assert(o.StartUp(), IsNil)

	st.Lock()
	c.Assert(st.Get("start-of-operation-time", &opTime), IsNil)
}

func (ovs *overlordSuite) TestEnsureLoopPruneDoesntAbortShortlyAfterStartOfOperation(c *C) {
	w, restoreTicker := fakePruneTicker()
	defer restoreTicker()

	// use real overlord, we need device manager to be there
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	// avoid immediate transition to Done due to unknown kind
	o.TaskRunner().AddHandler("bar", func(t *state.Task, _ *tomb.Tomb) error {
		return &state.Retry{}
	}, nil)

	st := o.State()
	st.Lock()

	// start of operation time is 50min ago, this is less then abort limit
	opTime := time.Now().Add(-50 * time.Minute)
	st.Set("start-of-operation-time", opTime)

	// spawn time one month ago
	spawnTime := time.Now().AddDate(0, -1, 0)
	restoreTimeNow := state.FakeTime(spawnTime)

	t := st.NewTask("bar", "...")
	chg := st.NewChange("other-change", "...")
	chg.AddTask(t)

	restoreTimeNow()

	// validity
	c.Check(st.Changes(), HasLen, 1)

	st.Unlock()
	c.Assert(o.StartUp(), IsNil)

	// start the loop that runs the prune ticker
	o.Loop()
	w.tick(2)

	c.Assert(o.Stop(), IsNil)

	st.Lock()
	defer st.Unlock()
	c.Assert(st.Changes(), HasLen, 1)
	c.Check(chg.Status(), Equals, state.DoingStatus)
}

func (ovs *overlordSuite) TestEnsureLoopPruneAbortsOld(c *C) {
	// Ensure interval is not relevant for this test
	restoreEnsureIntv := overlord.FakeEnsureInterval(10 * time.Hour)
	defer restoreEnsureIntv()

	w, restoreTicker := fakePruneTicker()
	defer restoreTicker()

	// use real overlord, we need device manager to be there
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	// avoid immediate transition to Done due to having unknown kind
	o.TaskRunner().AddHandler("bar", func(t *state.Task, _ *tomb.Tomb) error {
		return &state.Retry{}
	}, nil)

	st := o.State()
	st.Lock()

	// start of operation time is a year ago
	opTime := time.Now().AddDate(-1, 0, 0)
	st.Set("start-of-operation-time", opTime)

	st.Unlock()
	c.Assert(o.StartUp(), IsNil)
	st.Lock()

	// spawn time one month ago
	spawnTime := time.Now().AddDate(0, -1, 0)
	restoreTimeNow := state.FakeTime(spawnTime)
	t := st.NewTask("bar", "...")
	chg := st.NewChange("other-change", "...")
	chg.AddTask(t)

	restoreTimeNow()

	// validity
	c.Check(st.Changes(), HasLen, 1)
	st.Unlock()

	// start the loop that runs the prune ticker
	o.Loop()
	w.tick(2)

	c.Assert(o.Stop(), IsNil)

	st.Lock()
	defer st.Unlock()

	// validity
	op, err := o.StartOfOperationTime()
	c.Assert(err, IsNil)
	c.Check(op.Equal(opTime), Equals, true)

	c.Assert(st.Changes(), HasLen, 1)
	// change was aborted
	c.Check(chg.Status(), Equals, state.HoldStatus)
}

func (ovs *overlordSuite) TestCheckpoint(c *C) {
	oldUmask := syscall.Umask(0)
	defer syscall.Umask(oldUmask)

	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	s := o.State()
	s.Lock()
	s.Set("mark", 1)
	s.Unlock()

	st, err := os.Stat(ovs.statePath)
	c.Assert(err, IsNil)
	c.Assert(st.Mode(), Equals, os.FileMode(0600))

	c.Check(ovs.statePath, testutil.FileContains, `"mark":1`)
}

type sampleManager struct {
	ensureCallback func()
}

func newSampleManager(s *state.State, runner *state.TaskRunner) *sampleManager {
	sm := &sampleManager{}

	runner.AddHandler("runMgr1", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.Set("runMgr1Mark", 1)
		return nil
	}, nil)
	runner.AddHandler("runMgr2", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.Set("runMgr2Mark", 1)
		return nil
	}, nil)
	runner.AddHandler("runMgrEnsureBefore", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.EnsureBefore(20 * time.Millisecond)
		return nil
	}, nil)
	runner.AddHandler("runMgrForever", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.EnsureBefore(20 * time.Millisecond)
		return &state.Retry{}
	}, nil)
	runner.AddHandler("runMgrWCleanup", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.Set("runMgrWCleanupMark", 1)
		return nil
	}, nil)
	runner.AddCleanup("runMgrWCleanup", func(t *state.Task, _ *tomb.Tomb) error {
		s := t.State()
		s.Lock()
		defer s.Unlock()
		s.Set("runMgrWCleanupCleanedUp", 1)
		return nil
	})

	return sm
}

func (sm *sampleManager) Ensure() error {
	if sm.ensureCallback != nil {
		sm.ensureCallback()
	}
	return nil
}

func (ovs *overlordSuite) TestTrivialSettle(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(1 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	s := o.State()
	sm1 := newSampleManager(s, o.TaskRunner())
	o.AddManager(sm1)
	o.AddManager(o.TaskRunner())

	defer o.Engine().Stop()

	s.Lock()
	defer s.Unlock()

	chg := s.NewChange("chg", "...")
	t1 := s.NewTask("runMgr1", "1...")
	chg.AddTask(t1)

	s.Unlock()
	o.Settle(5 * time.Second)
	s.Lock()
	c.Check(t1.Status(), Equals, state.DoneStatus)

	var v int
	err := s.Get("runMgr1Mark", &v)
	c.Check(err, IsNil)
}

func (ovs *overlordSuite) TestSettleNotConverging(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(1 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	s := o.State()
	sm1 := newSampleManager(s, o.TaskRunner())
	o.AddManager(sm1)
	o.AddManager(o.TaskRunner())

	defer o.Engine().Stop()

	s.Lock()
	defer s.Unlock()

	chg := s.NewChange("chg", "...")
	t1 := s.NewTask("runMgrForever", "1...")
	chg.AddTask(t1)

	s.Unlock()
	err := o.Settle(250 * time.Millisecond)
	s.Lock()

	c.Check(err, ErrorMatches, `Settle is not converging`)

}

func (ovs *overlordSuite) TestSettleChain(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(1 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	s := o.State()
	sm1 := newSampleManager(s, o.TaskRunner())
	o.AddManager(sm1)
	o.AddManager(o.TaskRunner())

	defer o.Engine().Stop()

	s.Lock()
	defer s.Unlock()

	chg := s.NewChange("chg", "...")
	t1 := s.NewTask("runMgr1", "1...")
	t2 := s.NewTask("runMgr2", "2...")
	t2.WaitFor(t1)
	chg.AddAll(state.NewTaskSet(t1, t2))

	s.Unlock()
	o.Settle(5 * time.Second)
	s.Lock()
	c.Check(t1.Status(), Equals, state.DoneStatus)
	c.Check(t2.Status(), Equals, state.DoneStatus)

	var v int
	err := s.Get("runMgr1Mark", &v)
	c.Check(err, IsNil)
	err = s.Get("runMgr2Mark", &v)
	c.Check(err, IsNil)
}

func (ovs *overlordSuite) TestSettleChainWCleanup(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(1 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	s := o.State()
	sm1 := newSampleManager(s, o.TaskRunner())
	o.AddManager(sm1)
	o.AddManager(o.TaskRunner())

	defer o.Engine().Stop()

	s.Lock()
	defer s.Unlock()

	chg := s.NewChange("chg", "...")
	t1 := s.NewTask("runMgrWCleanup", "1...")
	t2 := s.NewTask("runMgr2", "2...")
	t2.WaitFor(t1)
	chg.AddAll(state.NewTaskSet(t1, t2))

	s.Unlock()
	o.Settle(5 * time.Second)
	s.Lock()
	c.Check(t1.Status(), Equals, state.DoneStatus)
	c.Check(t2.Status(), Equals, state.DoneStatus)

	var v int
	err := s.Get("runMgrWCleanupMark", &v)
	c.Check(err, IsNil)
	err = s.Get("runMgr2Mark", &v)
	c.Check(err, IsNil)

	err = s.Get("runMgrWCleanupCleanedUp", &v)
	c.Check(err, IsNil)
}

func (ovs *overlordSuite) TestSettleExplicitEnsureBefore(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(1 * time.Minute)
	defer restoreIntv()
	o := overlord.Fake()

	s := o.State()
	sm1 := newSampleManager(s, o.TaskRunner())
	sm1.ensureCallback = func() {
		s.Lock()
		defer s.Unlock()
		v := 0
		s.Get("ensureCount", &v)
		s.Set("ensureCount", v+1)
	}

	o.AddManager(sm1)
	o.AddManager(o.TaskRunner())

	defer o.Engine().Stop()

	s.Lock()
	defer s.Unlock()

	chg := s.NewChange("chg", "...")
	t := s.NewTask("runMgrEnsureBefore", "...")
	chg.AddTask(t)

	s.Unlock()
	o.Settle(5 * time.Second)
	s.Lock()
	c.Check(t.Status(), Equals, state.DoneStatus)

	var v int
	err := s.Get("ensureCount", &v)
	c.Check(err, IsNil)
	c.Check(v, Equals, 2)
}

func (ovs *overlordSuite) TestRequestRestartNoHandler(c *C) {
	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir})
	c.Assert(err, IsNil)

	st := o.State()
	st.Lock()
	defer st.Unlock()

	restart.Request(st, restart.RestartDaemon)
}

type testRestartHandler struct {
	restartRequested  restart.RestartType
	rebootState       string
	rebootVerifiedErr error
}

func (rb *testRestartHandler) HandleRestart(t restart.RestartType) {
	rb.restartRequested = t
}

func (rb *testRestartHandler) RebootAsExpected(_ *state.State) error {
	rb.rebootState = "as-expected"
	return rb.rebootVerifiedErr
}

func (rb *testRestartHandler) RebootDidNotHappen(_ *state.State) error {
	rb.rebootState = "did-not-happen"
	return rb.rebootVerifiedErr
}

func (ovs *overlordSuite) TestRequestRestartHandler(c *C) {
	rb := &testRestartHandler{}

	o, err := overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, IsNil)

	st := o.State()
	st.Lock()
	defer st.Unlock()

	restart.Request(st, restart.RestartDaemon)

	c.Check(rb.restartRequested, Equals, restart.RestartDaemon)
}

func (ovs *overlordSuite) TestVerifyRebootNoPendingReboot(c *C) {
	fakeState := []byte(fmt.Sprintf(`{"data":{"patch-level":%d,"patch-sublevel":%d,"some":"data","refresh-privacy-key":"0123456789ABCDEF"},"changes":null,"tasks":null,"last-change-id":0,"last-task-id":0,"last-lane-id":0}`, patch.Level, patch.Sublevel))
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	rb := &testRestartHandler{}

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, IsNil)

	c.Check(rb.rebootState, Equals, "as-expected")
}

func (ovs *overlordSuite) TestVerifyRebootOK(c *C) {
	fakeState := []byte(fmt.Sprintf(`{"data":{"patch-level":%d,"patch-sublevel":%d,"some":"data","refresh-privacy-key":"0123456789ABCDEF","system-restart-from-boot-id":%q},"changes":null,"tasks":null,"last-change-id":0,"last-task-id":0,"last-lane-id":0}`, patch.Level, patch.Sublevel, "boot-id-prev"))
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	rb := &testRestartHandler{}

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, IsNil)

	c.Check(rb.rebootState, Equals, "as-expected")
}

func (ovs *overlordSuite) TestVerifyRebootOKButError(c *C) {
	fakeState := []byte(fmt.Sprintf(`{"data":{"patch-level":%d,"patch-sublevel":%d,"some":"data","refresh-privacy-key":"0123456789ABCDEF","system-restart-from-boot-id":%q},"changes":null,"tasks":null,"last-change-id":0,"last-task-id":0,"last-lane-id":0}`, patch.Level, patch.Sublevel, "boot-id-prev"))
	err := os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	e := errors.New("boom")
	rb := &testRestartHandler{rebootVerifiedErr: e}

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, Equals, e)

	c.Check(rb.rebootState, Equals, "as-expected")
}

func (ovs *overlordSuite) TestVerifyRebootIsMissing(c *C) {
	curBootID, err := osutil.BootID()
	c.Assert(err, IsNil)

	fakeState := []byte(fmt.Sprintf(`{"data":{"patch-level":%d,"patch-sublevel":%d,"some":"data","refresh-privacy-key":"0123456789ABCDEF","system-restart-from-boot-id":%q},"changes":null,"tasks":null,"last-change-id":0,"last-task-id":0,"last-lane-id":0}`, patch.Level, patch.Sublevel, curBootID))
	err = os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	rb := &testRestartHandler{}

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, IsNil)

	c.Check(rb.rebootState, Equals, "did-not-happen")
}

func (ovs *overlordSuite) TestVerifyRebootIsMissingError(c *C) {
	curBootID, err := osutil.BootID()
	c.Assert(err, IsNil)

	fakeState := []byte(fmt.Sprintf(`{"data":{"patch-level":%d,"patch-sublevel":%d,"some":"data","refresh-privacy-key":"0123456789ABCDEF","system-restart-from-boot-id":%q},"changes":null,"tasks":null,"last-change-id":0,"last-task-id":0,"last-lane-id":0}`, patch.Level, patch.Sublevel, curBootID))
	err = os.WriteFile(ovs.statePath, fakeState, 0600)
	c.Assert(err, IsNil)

	e := errors.New("boom")
	rb := &testRestartHandler{rebootVerifiedErr: e}

	_, err = overlord.New(&overlord.Options{PebbleDir: ovs.dir, RestartHandler: rb})
	c.Assert(err, Equals, e)

	c.Check(rb.rebootState, Equals, "did-not-happen")
}

func (ovs *overlordSuite) TestOverlordCanStandby(c *C) {
	restoreIntv := overlord.FakeEnsureInterval(10 * time.Millisecond)
	defer restoreIntv()
	o := overlord.Fake()
	witness := &witnessManager{
		state:          o.State(),
		expectedEnsure: 3,
		ensureCalled:   make(chan struct{}),
	}
	o.AddManager(witness)

	c.Assert(o.StartUp(), IsNil)

	// can only standby after loop ran once
	c.Assert(o.CanStandby(), Equals, false)

	o.Loop()
	defer o.Stop()

	select {
	case <-witness.ensureCalled:
	case <-time.After(2 * time.Second):
		c.Fatal("Ensure calls not happening")
	}

	c.Assert(o.CanStandby(), Equals, true)
}

func (ovs *overlordSuite) TestStartOfOperationTimeAlreadySet(c *C) {
	o := overlord.Fake()
	st := o.State()
	st.Lock()
	defer st.Unlock()

	op := time.Now().AddDate(0, -1, 0)
	st.Set("start-of-operation-time", op)

	operationTime, err := o.StartOfOperationTime()
	c.Assert(err, IsNil)
	c.Check(operationTime.Equal(op), Equals, true)
}

func (s *overlordSuite) TestStartOfOperationSetTime(c *C) {
	o := overlord.Fake()
	st := o.State()
	st.Lock()
	defer st.Unlock()

	now := time.Now().Add(-1 * time.Second)
	overlord.FakeTimeNow(func() time.Time {
		return now
	})

	operationTime, err := o.StartOfOperationTime()
	c.Assert(err, IsNil)
	c.Check(operationTime.Equal(now), Equals, true)

	// repeated call returns already set time
	prev := now
	now = time.Now().Add(-10 * time.Hour)
	operationTime, err = o.StartOfOperationTime()
	c.Assert(err, IsNil)
	c.Check(operationTime.Equal(prev), Equals, true)
}
