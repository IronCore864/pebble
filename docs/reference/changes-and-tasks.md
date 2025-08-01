# Changes and tasks

When Pebble performs a (potentially invasive or long-running) operation such as starting or stopping a service, it records a "change" object with one or more "tasks" in it.

## How it works

The daemon records this state in a JSON file on disk at `$PEBBLE/.pebble.state`.

If the environment variable `PEBBLE_PERSIST` is set to "never", Pebble will only keep the state in memory without persisting it to a file.

## Commands

- {ref}`reference_pebble_changes_command`
- {ref}`reference_pebble_tasks_command`
