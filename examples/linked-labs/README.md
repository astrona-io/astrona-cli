# Linked labs — two labs side by side

`app` links to `idp` (`links:` in `app/config.yaml`). One command starts both:

```sh
astrona run -c examples/linked-labs/app      # starts idp first, then app
astrona docs question -c examples/linked-labs/app
astrona status linked-app                     # shows the link and its state
astrona submit -c examples/linked-labs/app
astrona test -c examples/linked-labs/app      # test copies of both, torn down after
astrona destroy linked-app && astrona destroy linked-idp
```

See the [Linked labs guide](../../docs/guides/linked-labs.md).
