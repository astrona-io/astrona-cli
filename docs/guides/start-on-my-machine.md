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

## What the website can ask for

Only these commands, each for one catalog lab named like
`ATS000/section-010/module-01/lab-01`:

| Request | Runs |
| --- | --- |
| Start | `astrona run <lab> --yes --no-browser`, taking over the lab session the website created |
| Status | `astrona status <lab>` |
| Submit | `astrona submit <lab> --keep` |
| Reset | `astrona reset <lab> --yes` |
| Destroy | `astrona destroy <lab>` |
| Stop / Start | `astrona stop <lab>` / `astrona start <lab>` |

`astrona connect` checks the command and the lab name before anything runs and
refuses everything else: shell commands, local paths, URLs and other options
never travel over the connection. Each command runs without a terminal, in an
empty working directory, one at a time. Anything that would need an answer
from you fails rather than waiting. Accept the [CLI terms](cli-terms.md) once
at a terminal first.

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
connection over, and the first one stops.
