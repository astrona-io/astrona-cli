# Authoring a Lab

A lab is a directory with a `config.yaml` (or whatever `--file` points at) plus whatever scripts/manifests/docs it references. This walks through building one from scratch, using [`examples/k8s-basics-01`](https://github.com/astrona-io/astrona-cli/tree/main/examples/k8s-basics-01) as the worked reference.

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
