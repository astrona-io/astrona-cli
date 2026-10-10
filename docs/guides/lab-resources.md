# Lab Resources

A lab often needs files on your machine: a script that sets something up, a YAML file you're meant to fix, a small program that generates traffic. Instead of copying them out of the lab's docs, the lab ships them as **resources** and `astrona resource` (short: `astrona res`) gives them to you.

## When a lab starts

`astrona run` copies the lab's resources to `~/.astrona/resources/<lab>/` and lists them — **nothing runs** until you ask:

```text
Resources (3) — copied to ~/.astrona/resources/astro-ats-014-lab-010-01, nothing has run:
  broken-deployment  The Deployment you'll fix in step 2             file
  load-test          Generates traffic so you can watch the HPA      go run .
  setup-db           Creates the demo database the task starts from  bash setup-db.sh
```

`astrona reset` copies them again; `astrona destroy` removes them (`--keep-resources` keeps them).

## Using them

```sh
astrona res                         # list the lab's resources
astrona res run setup-db            # run one
astrona res show broken-deployment  # print it
astrona res copy broken-deployment  # copy it into this folder to edit (--force replaces)
astrona res path broken-deployment  # its path, for your own commands:
kubectl diff -f $(astrona res path broken-deployment)
astrona res run load-test -- --rps 50   # arguments after the name go to the resource
```

How `run` runs a resource:

| Resource | Runs as | In |
|---|---|---|
| has a `run:` command in the lab | that command (split on spaces, no shell) | the resource's folder |
| `*.sh` | `bash <file>` | your current folder |
| `*.yaml` / `*.yml` | `kubectl apply -f <file>` | your current folder |
| an executable starting with `#!` | the file itself | your current folder |
| anything else, and folders without `run:` | — only `show` / `copy` / `path` | |

In a kind lab every resource runs with **`KUBECONFIG` set to the lab's own kubeconfig** (plus its linked clusters'), exactly like [`astrona shell`](../reference/cli/astrona_shell.md) — so a resource's `kubectl` never reaches your other clusters, whatever your current context is. `$ASTRONA_LAB` holds the lab and `$ASTRONA_RESOURCE_DIR` the folder with all its resources (one resource can use another). Running resources inside a qemu lab's VM isn't supported yet — copy them and use `astrona ssh`.

## Several labs, several terminals

`astrona res` picks the lab in this order:

1. `--lab <lab>` (a running lab's name or a catalog name)
2. the lab of the `astrona shell` you're in (`$ASTRONA_LAB`)
3. the lab of `-c` / `--git`, or the one picked with [`astrona use`](../reference/cli/astrona_use.md)
4. the only lab that has resources

Otherwise it stops and lists the labs — it never guesses. With two labs open, run `astrona shell <lab>` in each terminal: every `astrona res` in that terminal then works on its lab.

## For lab authors

Put the files in a `resources/` folder next to `config.yaml`; an optional `resources:` list adds descriptions and run commands. See [`resources` in the lab config reference](../reference/lab-config.md#resources).
