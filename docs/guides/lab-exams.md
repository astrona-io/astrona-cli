# Full Lab Exams

A full lab exam, such as the CKS practice exam, is a set of tasks from the
lab catalog that you work through against the clock, on several Kubernetes
clusters that run on this computer. `astrona exam` builds the clusters,
shows the tasks, handles breaks, and grades everything at the end. You see
your score on the exam page, not in the terminal.

You need to be signed in (`astrona login`), and your container engine needs
enough memory for every cluster at once.

## Start an exam

```bash
astrona exam start cks
```

Before anything is created, astrona checks the exam's clusters against your
container engine's memory. The clusters, plus any kind clusters already
running, must fit in 75% of it. If they don't, astrona prints how much the
exam needs and stops. Nothing is counted.

If they fit, the tasks are drawn and astrona:

1. builds every cluster in parallel (`--parallel` sets how many at once);
2. applies each task's setup to the cluster it is assigned to;
3. adds a kubectl context for each cluster: `exam-1`, `exam-2`, and so on;
4. opens the exam page (`--no-browser` prints its address instead).

The exam page follows these steps. If one fails, or you press Ctrl+C, every
exam cluster is deleted and the attempt doesn't count. Run
`astrona exam start` again for a new draw.

**The clock does not run while the clusters are prepared.** It starts when
you press **Begin exam** on the exam page.

## Work on the tasks

```bash
astrona exam task 1
```

prints a task with its weight and the context it uses:

```text
Task 7 (6.67%)   Use context: kubectl config use-context exam-3
```

astrona does not switch the context for you. Run the `use-context` command
yourself before you start a task, as in the real exam.

`astrona exam next` and `astrona exam prev` move one task forward or back.
Take the tasks in any order. When you move away from a task, astrona grades
it quietly and keeps that result, so a crash later doesn't lose your work.
You don't see these results.

`astrona exam status` shows the time left, the breaks used, and every task
with its weight and context.

## Breaks

```bash
astrona exam break
```

stops the clock and pauses every exam cluster, so kubectl against an
`exam-<n>` context waits until the break ends. The exam page lists how many
breaks you have and how long each may last (for CKS, 3 breaks of at most 15
minutes). A break ends when you run `astrona exam resume`, or by itself at
its limit.

## Submit

```bash
astrona exam submit
```

asks for confirmation, then grades every task on its cluster, sends the
results, deletes the exam clusters and the `exam-<n>` contexts, and switches
kubectl back to the context you had before the exam. When the time runs out,
the same happens without you: a background watchdog started by
`astrona exam start` grades and cleans up at the deadline.

If results can't be sent (for example, you're offline), astrona keeps them
and sends them with the next `astrona exam` command.

## Abandon

```bash
astrona exam abandon
```

ends the exam at once and deletes its clusters, without grading the tasks
again. After Begin exam, the attempt is scored from the results already sent
(the ones kept when you moved between tasks). Before Begin exam, it is
cancelled and doesn't count.

## After a crash or a reboot

```bash
astrona exam resume
```

checks the exam's clusters, rebuilds any that are gone with their tasks'
setups, and restarts the watchdog. The clock keeps running while it
rebuilds, and work on a rebuilt cluster is lost.

To continue on another computer an exam you started elsewhere, name it:
`astrona exam resume cks`. Every cluster is prepared again there.

## What runs on this computer

- One kind cluster per exam cluster, named `astro-exam-<attempt>-<n>`.
  Don't delete them during the exam.
- The `exam-<n>` kubectl contexts in your kubeconfig. If you already have a
  context with one of those names that doesn't belong to an exam, start
  refuses until you rename it.
- The exam's record in `~/.astrona/exams/current.json`, and a watchdog
  process (`astrona exam-watchdog`) that logs to `~/.astrona/labs/`.
