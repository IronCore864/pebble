Start: Install Pebble executable

1. Visit the [latest Pebble release](https://github.com/canonical/pebble/releases/latest)
to get the latest release tag.
1. Download the file containing the Pebble executable. Be sure to replace
`v1.12.0` with the latest release tag and `amd64` to match your machine
architecture in the command below.
   ```bash
   wget https://github.com/canonical/pebble/releases/download/v1.12.0/pebble_v1.12.0_linux_amd64.tar.gz
   ```
1. Extract the contents of the downloaded file.
   ```bash
   tar zxvf pebble_v1.12.0_linux_amd64.tar.gz
   ```
1. Install the Pebble binary. Make sure that {file}`/usr/local/bin/` is included
in your system `PATH` variable.
   ```bash
   sudo mv pebble /usr/local/bin/
   ```

End: Install Pebble executable

Start: Verify Pebble installation

Once installation is complete, verify that `pebble` has been installed
correctly by running:

```bash
pebble
```

This should produce output similar to the following:

```{terminal}
   :input: pebble
   :user: user
   :host: host
   :dir: ~

Pebble lets you control services and perform management actions on
the system that is running them.

Usage: pebble <command> [<options>...]

...

```

End: Verify Pebble installation
