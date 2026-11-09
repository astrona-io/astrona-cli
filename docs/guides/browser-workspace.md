# Browser Workspace

The browser workspace shows a Kind lab's task next to a terminal on the lab, in
your own browser. `astrona` serves the page itself, on `127.0.0.1` only; nothing
leaves your machine.

![The browser workspace: the task on the left, a terminal on the lab on the right](../assets/browser-workspace.png)

## Open a lab in the browser

Run a Kind lab interactively:

```sh
astrona run ./labs/my-lab
```

Once the lab is trusted, `astrona` asks "Open this lab in your browser?". Answer
`yes` for this run, or `always` / `never` to save the answer as the `browser`
setting (`astrona settings set browser ask` asks again). `--browser` and
`--no-browser` answer in advance:

```sh
astrona run ./labs/my-lab --browser
```

The lab is set up as usual. Then `astrona` starts the workspace server (or
reuses a running one), prints a link and opens it. The link carries a
single-use code that is valid for two minutes; the page removes it from the
address bar after loading. A link that was already used, or is older than two
minutes, shows **Link expired**: run `astrona ui status` for a fresh one.

## Before you start

The first page shows the lab's prerequisites, its time limit and number of
questions, and the rules for an exam. **Environment ready** means the lab
finished setting up.

![The intro page with the rules and the Start button](../assets/browser-workspace-intro.png)

An exam's timer starts when you click **Start**, not when the lab is ready. From
then on it keeps running if you close the page or lose the connection; the top
bar counts down to the deadline.

## The workspace

- **Task** (left): the question text, rendered from the lab's docs.
- **Terminal** (right): a shell on the lab with `kubectl` pointed at its
  cluster. Keyboard focus moves into the terminal after **Start** and when the
  page opens. Press <kbd>F6</kbd> to leave the terminal; <kbd>Shift</kbd>+<kbd>F6</kbd>
  works too. The skip links at the top move focus between task and terminal.
- **Divider**: drag it, or focus it and use the arrow keys (<kbd>Shift</kbd> for
  bigger steps, <kbd>Home</kbd> / <kbd>End</kbd> for the limits). The task
  panel starts at 25% of the width and can take 15% to 70%; it never gets
  narrower than 280 pixels. The browser remembers the width you chose.

Submit from a terminal with `astrona submit` when you are done.

### The terminal

The shell runs on your machine, held by the workspace server, not by the page:

- **Reload or close the page**: the shell keeps running. Opening the page again
  reattaches to it and replays its recent output.
- **Another tab**: opening the workspace in a second tab moves the terminal
  there. The first tab shows "This terminal was opened in another tab." with
  **Use it here** to take it back.
- **`exit`**: ends the shell. **Open a new shell** starts another one; files in
  the lab are kept.
- **Connection lost**: see below.

The terminal draws with WebGL, so programs that use 24-bit ("truecolor")
colours show them. Where WebGL is not available, it falls back to a renderer
that shows those colours as the terminal's default colours; 16 and 256-colour
output looks the same either way.

### Connection lost

When the page cannot reach the workspace server for more than a second or two
(the server stopped or hangs, or the computer slept), a dialog covers the
workspace and the page behind it is blurred, so nothing typed goes into a
terminal that cannot receive it:

![The connection-lost dialog over the blurred workspace](../assets/browser-workspace-connection-lost.png)

The page keeps reconnecting and shows the attempt number. The dialog cannot be
closed; it goes away by itself once the server answers, and you continue in
the same shell, with its output and the task as they were, and the keyboard
focus back where it was. Shorter drops do not show it.

If it does not come back, check with `astrona ui status` that the server is
running. If it is not, `astrona ui <lab>` starts it again and prints a new
link; the lab environment and its files are still there. A page from the
earlier server then shows **Session ended**.

With reduced motion or reduced transparency set in the system, the workspace
is dimmed rather than blurred.

## Stop the workspace

```sh
astrona ui status   # address, connections, open terminals and a fresh link
astrona ui stop     # stops the workspace; labs keep running
```

After `astrona ui stop` an open workspace shows the connection-lost dialog. The
server also stops by itself after it has been idle for a while.

## Theme

The page follows your system's light or dark appearance.

## Security

The server only answers on `127.0.0.1`, refuses other `Host` and `Origin`
headers, and sends a Content Security Policy that allows no inline scripts or
styles. The page talks to it with a session token it gets in exchange for the
single-use launch code. See the [Workspace API](../reference/workspace-api.md)
for the details.
