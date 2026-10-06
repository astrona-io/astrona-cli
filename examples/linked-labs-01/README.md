# Linked labs — two clusters side by side

One lab, two kind clusters: the lab's own (where the student works) and `idp`, a linked cluster defined under `runtime.kind.labs` in `config.yaml`. The task is to log in against the idp from the lab's cluster. Everything for the idp cluster lives in `labs/idp/` (its `bootstrap/` here; a `testing/` folder would hold its part of a reference solution).

```sh
astrona run -c examples/linked-labs-01      # creates idp, then the lab
astrona docs question -c examples/linked-labs-01
astrona status linked-labs-01               # shows the linked cluster and its state
astrona submit -c examples/linked-labs-01   # grades both clusters
astrona test -c examples/linked-labs-01     # test copies of both, torn down after
astrona destroy -c examples/linked-labs-01  # removes both
```

See the [Linked Labs guide](../../docs/guides/linked-labs.md).
