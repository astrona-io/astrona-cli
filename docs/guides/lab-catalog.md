# Lab Catalog

You don't need a path or URL to start a lab: astrona knows the published trainings and every lab in them, by name.

```sh
astrona labs                                   # trainings
astrona labs ATS014                            # a training's labs
astrona labs --search "fault injection"        # search lab titles
astrona run ATS014/section-050/module-01/lab-01
```

A catalog name works wherever a command takes a lab: `astrona use ATS014/…` picks it for every command, `astrona run`, `submit`, `reset`, `test`, `validate` and `doctor ATS014/…`, and also the commands that otherwise take a running lab's name — `astrona status`, `shell`, `kubeconfig`, `destroy ATS014/…` and `astrona docs question ATS014/…`. That way the one name a student copies from astrona.io works for every step. Running-lab names (`my-lab`, `astro-my-lab`) and globs work as before: catalog names always contain a `/`, lab names never do.

## Where the catalog comes from

- **Published trainings** — repositories in the [astrona-io](https://github.com/astrona-io) organization with the `astrona-training` topic. astrona finds them with one GitHub search.
- **Your own** — `astrona labs add <git url>` adds any git repository with an `astrona.yaml` (GitHub repos are read directly, others are cloned). `astrona labs remove <git url>` takes it out again, and `astrona labs sources` shows everything the catalog reads.

The catalog is cached for an hour in `~/.astrona/catalog.json`; `--refresh` reads it again. When it can't be refreshed (offline), the last copy is used, with a warning. `ASTRONA_CATALOG_ORG` points it at another organization (empty: only your own sources).

## Lab names

A training's `astrona.yaml` lists its labs (`content` entries of `type: lab`, in `modules`, or in `sections` with their `modules` and `capstone`). A lab's name is the training id and the lab's path, without `sections/` and `labs/`:

```
sections/section-050/module-01/labs/lab-01   →   ATS014/section-050/module-01/lab-01
```

## Safety

A lab from the catalog is a remote lab: astrona fetches the training's repository with git and **asks for your trust** before running it, showing what it will do — like any `--git` lab (`--trust` approves it up front, e.g. in CI). Everything a manifest says is treated as data: a lab path that would leave the repository is rejected. A path that exists on your disk always wins over a catalog name.

## Publishing a training

Give the repository an `astrona.yaml`:

```yaml
training:
  id: ATS014
  title: "ICA: Traffic Management"
  description: …
modules:
  - id: module-010
    content:
      - type: lab
        title: "Route Requests By Header Lab"
        path: sections/section-010/module-01/labs/lab-01   # a lab directory with config.yaml
```

…and the `astrona-training` topic (in the astrona-io organization) — or tell people to `astrona labs add` its URL.
