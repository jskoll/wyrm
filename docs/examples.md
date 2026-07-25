# Examples

Full configs live in [`examples/`](https://github.com/jskoll/wyrm/tree/main/examples)
in the repo — copy one in as `.wyrm.toml` and adjust.

## [minimal.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/minimal.wyrm.toml)

The smallest useful config: one window, editor left, shell right.

```toml
[session]
root = "."          # session name defaults to this directory's name

[[windows]]
name = "main"

  [[windows.splits]]
  command = "nvim"

  [[windows.splits]]
  type = "h"        # split horizontally: new pane to the right
  size = 30         # new pane gets 30% of the width
```

## [basic.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/basic.wyrm.toml)

Legacy flat `panes` format across three windows (editor, tests, server),
with lifecycle hooks and a `startup_window`.

!!! warning "Deprecated format"
    This is the only example still using the flat `[[windows.panes]]` list,
    kept as a reference for existing configs. It and the `.tmuxconfig`
    filename are slated for removal in 1.0, and wyrm warns when it loads a
    config using either. New configs should use the `splits` tree — every
    other example below does.

## [nested-splits.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/nested-splits.wyrm.toml)

Demonstrates the split tree with nested `children`, `pre_window` interpreter
setup, and `startup_pane` targeting.

## [nodejs.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/nodejs.wyrm.toml)

A Node.js project layout: editor, dev server, and a test watcher window,
using the `splits` tree.

## [php-symfony.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/php-symfony.wyrm.toml)

A PHP/Symfony layout: editor, `symfony serve`, and a database window,
using the `splits` tree.

## [python.wyrm.toml](https://github.com/jskoll/wyrm/blob/main/examples/python.wyrm.toml)

A Python project layout: editor, test runner, and a REPL window,
using the `splits` tree.
