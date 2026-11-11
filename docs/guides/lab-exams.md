# Hands-on Exams

A hands-on exam, such as the CKS practice exam, is a set of tasks from the
lab catalog that you work through against the clock, on several Kubernetes
clusters that run on this computer. `astrona hands-on-exam` builds the clusters,
shows the tasks, handles breaks, and grades everything at the end. You see
your score on the exam page, not in the terminal.

`astrona exam` still works as another name for `astrona hands-on-exam`, so
scripts written for it keep running.

You need to be signed in (`astrona login`), and your container engine needs
enough memory for every cluster at once.

## Start an exam

```bash
astrona hands-on-exam start cks
```

Before anything is created, astrona checks the exam's clusters against your
container engine's memory. The clusters, plus any kind clusters already
running, must fit in 75% of it. If they don't, astrona prints how much the
exam needs and stops. Nothing is counted.

If they fit, the tasks are drawn and astrona:

1. builds every cluster in parallel (`--parallel` sets how many at once);
2. applies each task's setup to the cluster it is assigned to, with the
   lab's own CA first when the lab has one (`sharedCA`). The setup scripts
   and later the grading run with that cluster's `exam-<n>` context (see
   [runtimes](../concepts/runtimes.md#kind-default));
3. adds a kubectl context for each cluster: `exam-1`, `exam-2`, and so on;
4. opens the exam page (`--no-browser` prints its address instead).

The exam page follows these steps. If one fails, or you press Ctrl+C, every
exam cluster is deleted and the attempt doesn't count. Run
`astrona hands-on-exam start` again for a new draw.

**The clock does not run while the clusters are prepared.** It starts when
you press **Begin exam** on the exam page.

## Work on the tasks

```bash
astrona hands-on-exam task 1
```

prints a task with its weight and the context it uses:

```text
Task 7 (6.67%)   Use context: kubectl config use-context exam-3
```

astrona does not switch the context for you. Run the `use-context` command
yourself before you start a task, as in the real exam.

`astrona hands-on-exam next` and `astrona hands-on-exam prev` move one task forward or back.
Take the tasks in any order. When you move away from a task, astrona grades
it quietly and keeps that result, so a crash later doesn't lose your work.
You don't see these results.

`astrona hands-on-exam status` shows the time left first, in a banner, then the
breaks used and every task with its weight and context:

```text
==============================================
  TIME LEFT  1:23:05  (until 16:40)
==============================================
```

## Time-left notices

While the exam runs, the watchdog shows a desktop notification at 60, 30,
15, 5 and 1 minute(s) left ("<exam name>: 5 minutes left"), and "Time is
up — grading your tasks" when the clock runs out. On macOS it uses
`osascript` (`display notification`), on Linux `notify-send`. Without that
tool, or on another system, there is no notification and nothing else
changes; a notification never holds up the watchdog. Each notice is shown
once; if time is added back, a later one is shown again when it is reached.

## Breaks

```bash
astrona hands-on-exam break
```

stops the clock and pauses every exam cluster, so kubectl against an
`exam-<n>` context waits until the break ends. The exam page lists how many
breaks you have and how long each may last (for CKS, 3 breaks of at most 15
minutes). A break ends when you run `astrona hands-on-exam resume`, or by itself at
its limit.

## Submit

```bash
astrona hands-on-exam submit
```

asks for confirmation, then grades every task on its cluster, sends the
results, deletes the exam clusters and the `exam-<n>` contexts, and switches
kubectl back to the context you had before the exam. When the time runs out,
the same happens without you: a background watchdog started by
`astrona hands-on-exam start` grades and cleans up at the deadline.

Every result carries how it was graded: `switch` (you moved away from the
task), `submit`, `deadline` (the watchdog at time up), `check` or `wake`
(see below).

## How an exam ends

| How it ended | Shown as | Counts as an attempt |
| --- | --- | --- |
| You submit | submitted | yes |
| You abandon after Begin exam | ended early, scored from the results so far | yes |
| The clock runs out while you were working | time up | yes |
| The clock runs out after you had left | cancelled: no activity before time ran out | no |
| Abandoned before Begin exam, or never begun | cancelled | no |

Astrona decides between "time up" and "no activity" from your activity in
the last 30 minutes before the deadline, and from whether the deadline's
grading found more work done on a task than its earlier results.

## Abandon

```bash
astrona hands-on-exam abandon
```

ends the exam now and deletes its clusters. The tasks are not graded again:
after Begin exam, the exam is scored from the results already sent (the ones
kept when you moved between tasks) and it counts as an attempt. Before Begin
exam it is cancelled and doesn't count.

## Activity

Astrona needs to know whether you were still working when the time ran out.
astrona reports activity, with the time it happened and at most once a
minute, when:

- you run any `astrona hands-on-exam` command;
- the watchdog sees, once a minute, that one of your shell history files
  (`~/.zsh_history`, `~/.bash_history`, `~/.local/share/fish/fish_history`,
  or `$HISTFILE`) was written since it last looked, which means you ran a
  command. Only the file's modification time is read, never its contents.

It is a heuristic: a shell that saves its history only on exit is not seen
until then. Astrona also counts the results astrona sends as activity.

## Offline, asleep and late results

Every result, task switch, break and activity report is first saved in
`~/.astrona/exams/pending.json` and then sent, in order, with the time it
happened. When Astrona can't be reached, it waits there: the watchdog keeps
sending it (also after the exam has ended, until nothing waits), and so does
every `astrona` command. Astrona accepts a result graded before the end at
any time, even after the attempt was graded, so a result is never dropped
for being late. Astrona refuses only a result timed after the end, one timed
inside a break, or one for an unknown task; those are dropped with a
message. Anything still waiting after 7 days is given up. While you are
signed out, it waits for `astrona login`.

`astrona hands-on-exam break` and `astrona hands-on-exam resume` work offline too: the clusters
are paused or unpaused at once and Astrona is told later, with the time.

If your computer is asleep or off when the time runs out, nothing can change
on the exam clusters meanwhile. When the watchdog or any `astrona hands-on-exam`
command next runs and finds the exam ended, it grades every task then, as of
the end (source `wake`), sends the results and deletes the clusters. An
exam submitted or abandoned elsewhere is not graded again.

## After a crash or a reboot

```bash
astrona hands-on-exam resume
```

checks the exam's clusters, rebuilds any that are gone with their tasks'
setups, and restarts the watchdog. The clock keeps running while it
rebuilds, and work on a rebuilt cluster is lost.

To continue on another computer an exam you started elsewhere, name it:
`astrona hands-on-exam resume cks`. Every cluster is prepared again there.

## What runs on this computer

- One kind cluster per exam cluster, named `astro-exam-<attempt>-<n>`.
  Don't delete them during the exam.
- The `exam-<n>` kubectl contexts in your kubeconfig. If you already have a
  context with one of those names that doesn't belong to an exam, start
  refuses until you rename it.
- The exam's record in `~/.astrona/exams/current.json`, and a watchdog
  process (`astrona exam-watchdog`) that logs to `~/.astrona/labs/`.
