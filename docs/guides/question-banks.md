# Writing a Question Bank

A **question bank** is the knowledge check (quiz) at the end of a module: one YAML file with multiple-choice questions. It lives in the training repository next to the readings it asks about, and `astrona.yaml` lists it as a `quiz` item:

```yaml
      - type: quiz
        title: "Knowledge Check: Shift Traffic With Weighted Routing"
        path: sections/section-020/module-01/quiz.yaml
        pass_score: 70          # percent to pass, above 0 and at most 100 (default 70)
```

The bank has a [JSON Schema](https://cli.astrona.io/schema/question-bank.schema.json), so the bank doesn't need a copy of it in the training repository. Point your editor at it with the first line of the file:

```yaml
# yaml-language-server: $schema=https://cli.astrona.io/schema/question-bank.schema.json
```

## An example

```yaml
# yaml-language-server: $schema=https://cli.astrona.io/schema/question-bank.schema.json
schema_version: 1
kind: question_bank
id: ats014-020-01
course_id: ATS014
status: draft
provenance: Original practice material written for this course; not official exam questions.
scope:
  type: module
  id: section-020/module-01
  title: Shift Traffic With Weighted Routing
questions:
  - id: ats014-020-01-q01
    type: single_choice
    difficulty: beginner
    learning_objective: >-
      Write a VirtualService route with several weighted destinations.
    source_readings:
      - sections/section-020/module-01/course-01-split-traffic-with-weighted-destinations.md
    prompt: >-
      Where does the `weight` field go in a weighted `VirtualService` route?
    options:
      - id: a
        text: Inside `destination`, next to `host` and `subset`
        explanation: Kubernetes rejects the object, because `destination` has no `weight` field.
      - id: b
        text: Next to `destination`, in the same item of the `route` list
        explanation: Correct. Each `route` item has a `destination` and a `weight`.
      - id: c
        text: In the `DestinationRule` subset
        explanation: A `DestinationRule` defines subsets. It never splits traffic.
    correct_option: b
    explanation: The `weight` field sits next to `destination`, not inside it.
```

## Fields

| Field | Required | Description |
|---|---|---|
| `schema_version` | yes | `1` |
| `kind` | yes | `question_bank` |
| `id` | yes | Stable id of the bank |
| `course_id`, `exam_code` | no | The training and the exam the questions practise for |
| `status` | no | `draft` or `reviewed`. A `draft` bank works, but validation warns until someone has reviewed it |
| `provenance` | no | Where the questions come from. Say so when they are original material and not official exam questions |
| `scope` | no | What the bank covers: `type` (`module`, `section` or `domain`), `id` and `title` |
| `questions` | yes | 1–30 questions. The site uses the first 30 |

Each question:

| Field | Required | Description |
|---|---|---|
| `id` | yes | Unique in the bank. **Keep it stable once published**, so results stay comparable |
| `type` | no | `single_choice`, the only type (also the default) |
| `difficulty` | no | `beginner`, `intermediate` or `advanced` |
| `learning_objective` | no | What a student who answers right can do |
| `source_readings` | no | Repository paths of readings to read again. Each must exist; one that `astrona.yaml` doesn't list as a reading gets no "read it again" link |
| `prompt` | yes | The question. Markdown and `code` work |
| `options` | yes | 2–6 options: `id` and `text` (required), `explanation` (why it's right or wrong, shown after answering) |
| `correct_option` | yes | The `id` of one option |
| `explanation` | no | Why the answer is right |

A missing option or answer `explanation` is a warning, not an error — write them anyway: they're what teaches after a wrong answer.

## Checking it

```sh
astrona content validate ats ./ATS014
```

fails on what would keep a question off the site (a repeated id, a `correct_option` that isn't an option, a `source_readings` file that doesn't exist, fewer than 2 options) and warns about drafts and missing explanations. See [Authoring a Lab](authoring-a-lab.md#validating-a-training-repository-ats) for everything it checks.
