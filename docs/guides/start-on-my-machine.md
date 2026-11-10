# Start Labs from the Website

`astrona connect` keeps this computer connected to the Astrona site. While it
runs, **Start on my machine** on a lab page builds that lab here, and the page
follows its progress. You don't need to type the lab name or keep a terminal
next to the browser.

## Connect a computer

Sign in first, then connect:

```bash
astrona login
astrona connect
```

`astrona connect` prints a line when it connects and one for every request
from the website, followed by that command's own output. It keeps running
until you press Ctrl+C. If the network drops, it reconnects by itself: it
retries after about a second, waits up to a minute between tries, and spreads
the retries out at random. Each new connection tells the site how the previous
one ended, so support can see drops.

Pressing Ctrl+C while the website's command runs stops it the way Ctrl+C stops
`astrona run` at a terminal: a lab that is being built stops at the next step,
removes what this run created, and the lab page shows that the build failed.
`astrona connect` waits for that, up to two minutes, before it exits. Press
Ctrl+C a second time to quit at once; anything left over is then removed by
`astrona destroy`.

## Confirm each request

```bash
astrona connect --confirm
```

asks in the terminal before each request from the website runs, for example:

```text
The website asks to build and start ATS000/section-010/module-01/lab-01 on this computer. Allow? [y/N]
```

Type `y` to run it. Anything else, or no answer within two minutes, refuses
it, and the lab page shows that the request was declined on this computer.
Status requests run without asking because they change nothing. `--confirm`
needs a terminal, so it can't be combined with `--install`.

## Start with your login session

To have it start with your login session:

```bash
astrona connect --install     # macOS LaunchAgent, or Linux systemd user service
astrona connect --uninstall
```

The installed service writes to `~/.astrona/logs/connect.log`. It uses the
`PATH` of the shell you installed it from, so it finds Docker or Podman and
`kind` the same way. On Linux the user service runs while you are logged in;
`loginctl enable-linger` keeps it running after you log out. `--install` isn't
available on Windows yet.

Stopping the service (logging out, `--uninstall`) also lets a running lab
build clean up first. If another `astrona connect` takes the connection over,
the service exits with an error, and the service manager starts it again about
ten seconds later, so it takes the connection back. To run `astrona connect` in
a terminal for longer, run `astrona connect --uninstall` first. If this
computer is signed out, the service stops and stays stopped; sign in with
`astrona login`, then run `astrona connect --install` again.

## What the website can ask for

Only these commands, each for one catalog lab named like
`ATS000/section-010/module-01/lab-01`:

| Request | Runs |
| --- | --- |
| Start | `astrona run <lab> --yes --no-browser`, taking over the lab session the website created |
| Status | `astrona status <lab>` |
| Submit | `astrona submit <lab>`: it ends the attempt, pass or fail, and deletes the lab |
| Reset | `astrona reset <lab> --yes` |
| Destroy | `astrona destroy <lab>` |
| Stop / Start | `astrona stop <lab>` / `astrona start <lab>` |
| Renew | `astrona run renew <lab>` |

Renew gives a running playground its full time limit again, as
`astrona run renew` does at a terminal. For a lab that isn't a running
playground it fails with renew's own error, and the page shows it.

`astrona connect` checks the command and the lab name before anything runs and
refuses everything else: shell commands, local paths, URLs and other options
never travel over the connection. Each command runs without a terminal, in an
empty working directory, one at a time. Anything that would need an answer
from you fails rather than waiting. Accept the [CLI terms](cli-terms.md) once
at a terminal first.

## What the page shows while it builds

The lab page lists each stage of the build as it starts: the lab's cluster,
images, add-ons, setup scripts, manifests and readiness checks. For a lab with
linked clusters it also shows each cluster as it is created and when it is
ready, so a long multi-cluster build doesn't look stuck. Running
`astrona settings set parallel 3` on that computer creates up to three linked
clusters at a time, for the website as for `astrona run`.

## Your computers

```bash
astrona devices
```

lists every computer signed in to your account. This one is marked `*`, and
`online` means it is running `astrona connect`. To rename a computer, choose
the one lab pages preselect, or sign one out, go to **Settings → Devices** on
the website. Signing out a computer that is connected ends its connection and
removes its sign-in. `astrona connect` then stops and doesn't reconnect.

Only one `astrona connect` runs per computer. Starting a second one takes the
connection over, and the first one stops, unless it is the installed service,
which starts again and takes the connection back.
