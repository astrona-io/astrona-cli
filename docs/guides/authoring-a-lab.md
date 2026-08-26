# Authoring a Lab

A lab is a directory with a `config.yaml` (or whatever `--file` points at) plus whatever scripts/manifests/docs it references. This walks through building one from scratch, using [`examples/k8s-basics-01`](https://github.com/astrona-io/astrona-cli/tree/main/examples/k8s-basics-01) as the worked reference.

## Scaffolding New Content (For Teachers & Authors)

To make content creation fast, consistent, and collaborative, Astrona provides a built-in blueprint scaffolding command. By default, it dynamically pulls the template blueprint from a remote git repository based on the required `<type>` argument and replaces placeholders in filenames and contents with your customized arguments.

```sh
astrona content init <type> [path] [flags]
```

- **`<type>` (Required)**: Must be either `ats` (Astrona Training Series - hands-on sandbox lab) or `atp` (Astrona Training Path - course path/section).
- **`[path]` (Optional)**: The target directory to initialize. If omitted, this **defaults to the value of the `--slug` flag** (creating a directory named after your slug).

### Dynamic Blueprint Defaults

Depending on the content type specified, the tool clones a dedicated template repository if no custom `--repo` is defined:
- **`atp`**: Clones `git@github.com:astrona-io/training-path-blueprint.git`
- **`ats`**: Clones `git@github.com:astrona-io/training-sandbox-blueprint.git`

### Custom Scaffolding Arguments

You can supply the following customization arguments via command-line flags to template the newly initialized content:

| Flag | Variable | Default | Description |
|---|---|---|---|
| `-i, --path-id` | `path_id` | `ATPxxx` | Unique identifier of the training path/lab (e.g., `ATP010`) |
| `-s, --slug` | `slug` | `path-slug` | URL-safe and directory-friendly slug name of the path (becomes default folder name) |
| `-t, --title` | `title` | `Path Title` | Human-readable title of the path |
| `-d, --description` | `description` | *(engaging summary)* | A short summary of what the learner will achieve |
| `-v, --version` | `version` | `0.1.0` | Initial version of the training content |
| `-g, --language` | `language` | `en` | Learning language (e.g., `en`, `fr`, `de`) |
| `-a, --author-name` | `author_name` | `Author Name` | Name of the author/teacher creating the content |
| `-l, --license` | `license` | `Apache-2.0` | Content license (default is Apache 2.0 license) |
| `-r, --repo` | `repo` | `""` | Custom template repository to clone from (overrides default blueprints) |

### Example Scaffolding Commands

1. **Initialize an ATP (Training Path) into a custom directory:**
   ```sh
   astrona content init atp labs/my-custom-path \
     --path-id "ATP040" \
     --slug "linux-networking-basics" \
     --title "Linux Networking Basics" \
     --author-name "Alice Smith"
   ```

2. **Initialize an ATS (Lab Sandbox) defaulting to a folder named after your slug:**
   ```sh
   astrona content init ats \
     --path-id "ATS012" \
     --slug "virtio-disk-discovery" \
     --title "Virtio Disk Discovery Lab" \
     --author-name "Bob Jones"
   ```
   *This will automatically clone the sandbox template and create a new directory `./virtio-disk-discovery/`.*

## Validating Content (For Teachers & Authors)

Once a Training Path (`atp`) has been scaffolded and filled in, validate its `path.yaml` before publishing:

```sh
astrona content validate <type> <source> [flags]
```

- **`<type>` (Required)**: Currently only `atp` is implemented. `ats` is accepted at the CLI level but not yet validated.
- **`<source>` (Required)**: Either a local folder path (the raw content files, containing `path.yaml`) or a git repository URL (`https://`, `git@host:`, `ssh://`, or a `.git` suffix). Use `--git-ref` to pin a git source to a branch or tag.

Validation:

1. Confirms `path.yaml` exists at `<source>` and parses as YAML.
2. Checks `apiVersion` is `content.astrona.io/v1alpha1` and `kind` is `TrainingPath`.
3. Walks every `spec.stages[].content[]` entry and resolves its `repository` at `version` (a git branch or tag) via the same git cache used by `--git` elsewhere in this CLI — this both verifies you have access to the referenced content repo and warms the local cache for later steps.

```sh
astrona content validate atp ./paths/my-custom-path
astrona content validate atp https://github.com/astrona-io/kubernetes-networking-atp.git --git-ref v1.2.0
```

### `path.yaml` shape (ATP)

```yaml
apiVersion: content.astrona.io/v1alpha1
kind: TrainingPath
metadata:
  id: ATP001
  slug: linux-foundation-certified-sysadmin-lfcs
  title: "Linux Foundation Certified System Administrator (LFCS)"
  version: "1.0.0"
  authors:
    - name: "Alice Smith"
      email: "alice@example.com"
spec:
  stages:
    - id: "ATP001-STG001"
      title: "Operations Deployment"
      weight: 25
      content:
        - ref: ATS002
          repository: "git@github.com:astrona-io/ATS002.git"
          path: "."
          version: "1.0.0"
```

Each `stages[].content[]` entry references an external content repository (typically an ATS lab): `repository` is the git URL, `version` the branch/tag to check out, and `path` the subdirectory within it.

---

## 1. Metadata

```yaml
metadata:
  name: "k8s-basics-01"
  docs:
    prerequisites: "docs/prerequisites.md"    # knowledge/tooling needed before attempting
    examQuestion: "docs/exam-question.md"     # formal, self-contained task statement
    caseStudy: "docs/case-study.md"           # softer, hint-driven version of the same task
    guide: "docs/step-by-step-guide.md"       # full walkthrough with the answer
```

`metadata.name` becomes the cluster/VM name (prefixed `astro-` by astrona). `metadata.docs` are plain paths astrona doesn't render itself — they exist so a marketplace listing or terminal UI always knows which file is the prerequisites doc, the formal question, etc., instead of guessing from file names.

## 2. Pick a runtime

Omit `runtime:` entirely for a `kind` cluster (the default), or see [Runtimes](../concepts/runtimes.md) for a `qemu` VM-backed lab.

## 3. Bootstrap — what the student starts with

```yaml
bootstrap:
  init:
    - name: "echo"
      type: "file"
      source: "hello.sh"
  manifests: []
```

Keep this to whatever scaffolding the exercise needs *before* the student's own work — the actual task (creating the namespace, deploying the app, whatever it is) should not be pre-built here.

## 4. Testing — your reference solution

```yaml
testing:
  manifests:
    - name: "solution"
      type: "folder"
      source: "solution"
```

This only runs under `astrona test`, never for a student. Put the finished, correct solution here — the thing a student would produce if they solved the lab perfectly — so `astrona test` can prove your `validation` block actually passes against it.

## 5. Validation — how it's graded

```yaml
validation:
  checks:
    - name: "lab-ns namespace exists"
      type: "resourceExists"
      resource: "namespace/lab-ns"
    - name: "hello-config configmap exists"
      type: "resourceExists"
      resource: "configmap/hello-config -n lab-ns"
  script:
    name: "verify-configmap-content"
    description: "Custom script check: confirms the configmap's actual content, not just that it exists"
    type: "file"
    source: "validate.sh"
```

Start with `resourceExists`/`podReady` checks for "does the thing exist," and reach for a `script` when you need to verify actual content or behavior (see [Grading](../concepts/grading.md) for the full check-type reference). A validation script exits `0` for pass, non-zero for fail — write it the same way you'd write a test assertion.

## 6. Teardown

```yaml
teardown:
  init:
    - name: "dump-logs"
      type: "file"
      source: "teardown/dump-logs.sh"
  keepCluster: false
```

Usually just `keepCluster: false` (or omitted — that's the default). Add `init` scripts only if you need to capture state before the cluster disappears.

## 7. Prove it works

```sh
astrona test -c path/to/your-lab --junit-xml=report.xml
```

This is the whole point of the `testing` stage: it bootstraps your lab, applies your reference solution, submits it to the Proctor, and tears down — proving a student who does everything right will actually pass. Run this locally before publishing, and wire it into CI (see [CI Integration](ci-integration.md)) so a later edit to `validation` can't silently break the lab's own solution.

## Full schema reference

See [Lab Config Schema](../reference/lab-config.md) for every field, type, and default.
