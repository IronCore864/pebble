# Getting started with Pebble

This tutorial will guide you through a typical scenario to get you started with
Pebble.
You will install Pebble, run the Pebble daemon with a basic configuration,
manage a running service, and add on another configuration layer.

## Prerequisites

- A Linux machine.

## Download and install Pebble

The easiest way to install the latest Pebble release is by downloading the
executable.
If you prefer a different installation method, see {ref}`how_to_install_pebble`.

```{include} /reuse/common-blocks.md
   :start-after: Start: Install Pebble executable
   :end-before: End: Install Pebble executable
```

```{include} /reuse/common-blocks.md
   :start-after: Start: Verify Pebble installation
   :end-before: End: Verify Pebble installation
```

For more information, see {ref}`reference_pebble_help_command`.

## Configure Pebble

Now that Pebble has been installed, you can set up a basic configuration for the
service manager.

First, you will need to create a directory for storing the Pebble configuration
files (also known as _layers_), and add the `PEBBLE` environment variable to the
{file}`~/.bashrc` file.


```{code} bash
mkdir -p ~/PEBBLE/layers
export PEBBLE=$HOME/PEBBLE
echo "export PEBBLE=$HOME/PEBBLE" >> ~/.bashrc
```

Next, create a simple configuration layer file by running:

```{code-block}
:emphasize-lines: 8

echo """\
summary: Simple layer

description: |
    A simple layer.

services:
    http-server:
        override: replace
        summary: demo http server
        command: python3 -m http.server 8080
        startup: enabled
""" > $PEBBLE/layers/001-http-server.yaml
```

This creates a simple layer configuration that contains a single service
(running a basic HTTP server using the Python `http.server` module that listens
on port `8080`) and is stored in the {file}`$PEBBLE/layers/001-http-server.yaml`
file.

## Start the Pebble daemon

You are now ready to run the Pebble daemon. Return to your home directory and
run:

```bash
cd
pebble run
```

This command starts the Pebble daemon along with the HTTP server service, which
should be indicated in the output as below:

```{terminal}
   :input: pebble run
   :user: user
   :host: host
   :dir: ~

2024-06-10T04:46:22.507Z [pebble] Started daemon.
2024-06-10T04:46:22.513Z [pebble] POST /v1/services 3.273411ms 202
2024-06-10T04:46:22.516Z [pebble] Service "http-server" starting: python3 -m http.server 8080
```

You can verify that the HTTP server is running by opening a browser tab and
going to `localhost:8080`; you should see a directory listing of your home
directory.

## Monitor the status of services

While the Pebble daemon is running, you can retrieve the list of enabled
services along with the current status for each service by opening another
terminal tab / window and running:

```bash
pebble services
```

The "Current" status for the `http-server` service should be "active".

```{terminal}
   :input: pebble services
   :user: user
   :host: host
   :dir: ~

Service      Startup  Current  Since
http-server  enabled  active   today at 13:20 +08
```

You can stop the running `http-server` service by running:

```bash
pebble stop http-server
```

Verify that the service has stopped running by running the `services` command
again:
```bash
pebble services
```

The "Current" status for the `http-server` service should now be "inactive".

```{terminal}
   :input: pebble services
   :user: user
   :host: host
   :dir: ~

Service      Startup  Current   Since
http-server  enabled  inactive  today at 13:26 +08
```

You can verify this by visiting `localhost:8080` in your browser tab again; it
should display an error similar to "This site can't be reached".

To restart the service, run:

```bash
pebble start http-server
```

## Add a configuration layer

You can manage additional services with Pebble by adding another layer. To create
a new layer, run:

```{code-block} bash
:emphasize-lines: 8

echo """\
summary: Simple layer 2

description: |
    Yet another simple layer.

services:
    http-server-2:
        override: replace
        summary: demo http server 2
        command: python3 -m http.server 8081
        startup: enabled
""" > $PEBBLE/layers/002-another-http-server.yaml
```

This creates another layer that also contains a single service (running a basic
HTTP server using the Python `http.server` module that listens on port `8081`)
and is stored in the {file}`$PEBBLE/layers/002-another-http-server.yaml` file.

<!--
There should be a reference / how-to with more information on what is a
plan.
The help output for `pebble help add` could be more detailed.
 -->

Add the new layer to a Pebble plan:

```bash
pebble add layer1 $PEBBLE/layers/002-another-http-server.yaml
```

If the layer is added successfully, this should produce the following output:

```{terminal}
   :input: pebble add layer1 $PEBBLE/layers/002-another-http-server.yaml
   :user: user
   :host: host
   :dir: ~

Layer "layer1" added successfully from "/home/user/PEBBLE/layers/002-another-http-server.yaml"
```

## Sync the service state

Even though the service configuration has been updated with a new layer, the
services won't be automatically restarted. If we check the status of services:

```bash
pebble services
```

We can see the the `http-server-2` service has been added but is currently still
"inactive".

```{terminal}
   :input: pebble services
   :user: user
   :host: host
   :dir: ~

Service        Startup  Current   Since
http-server    enabled  active    today at 14:06 +08
http-server-2  enabled  inactive  -
```

To sync the service state with the new configuration, run:

```bash
pebble replan
```

If we check the status of services again, the `http-server-2` service should
have started and be shown as "active".

```{terminal}
   :input: pebble services
   :user: user
   :host: host
   :dir: ~

Service        Startup  Current  Since
http-server    enabled  active   today at 14:06 +08
http-server-2  enabled  active   today at 14:17 +08
```

## Exit the daemon

To exit the Pebble daemon, press {kbd}`Ctrl+c` in the first terminal where you
ran the `pebble run` command.

This sends an interrupt signal to the Pebble daemon and stops all the running
services.

## Next steps

- To learn more about running the Pebble daemon, see [How to run the daemon (server)](../how-to/run-the-daemon.md).
- To learn more about viewing, starting and stopping services, see [How to view, start, and stop services](../how-to/view-start-stop-services.md).
- To learn more about updating and restarting services, see [How to update and restart services](../how-to/update-restart-services.md).
- To learn more about configuring layers, see [How to configure layers](../how-to/configure-layers.md).
- To learn more about layer configuration options, read the [Layer specification](../reference/layer-specification.md).
