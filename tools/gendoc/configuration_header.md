---
description: This section describes the configuration parameters and their types for your IOTA core node.
keywords:
- IOTA Node 
- Configuration
- JSON
- Customize
- Config
- reference
---


# Configuration

IOTA core node uses a JSON standard format as a config file. If you are unsure about JSON syntax, you can find more information in the [official JSON specs](https://www.json.org).

You can change the path of the config file by using the `-c` or `--config` argument while executing `iota-core` executable.

For example:
```bash
iota-core -c config_example.json
```

You can always get the most up-to-date description of the config parameters by running:

```bash
iota-core -h --full
```

