# Lab Catalog

You don't need a path or URL to start a lab: astrona knows the published trainings and every lab in them, by name.

```sh
astrona labs                                   # trainings
astrona labs ATS014                            # a training's labs
astrona labs --search "fault injection"        # search lab titles
astrona run astrona.io/ATS014/section-050/module-01/lab-01
```

A lab's catalog name may start with the organization — `astrona.io/ATS014/…`, the form astrona.io shows and the course material uses — or with the GitHub owner, `astrona-io/ATS014/…`, or leave it out: `ATS014/…`. All three are the same lab; a name with another owner (`someone-else/ATS014/…`) is refused, so the name always says where the code comes from. A module's playground isn't listed in the training's manifest but is found by its folder: `astrona.io/ATS014/section-000/module-01/playground` → `sections/section-000/module-01/playground`.

A catalog name works wherever a command takes a lab: `astrona use astrona.io/ATS014/…` picks it for every command, `astrona run`, `submit`, `reset`, `test`, `validate` and `doctor ATS014/…`, and also the commands that otherwise take a running lab's name — `astrona status`, `shell`, `kubeconfig`, `destroy ATS014/…` and `astrona docs question ATS014/…`. That way the one name a student copies from astrona.io works for every step. Running-lab names (`my-lab`, `astro-my-lab`) and globs work as before: catalog names always contain a `/`, lab names never do.

## Your Astrona account

Catalog labs run locally without a login, including `astrona run ATS000/section-010/module-01/lab-01` and full resets. When signed out, astrona asks whether you want to continue without tracking progress and results on your Astrona account. The default answer is no; run `astrona login` to enable tracking. Pass `--without-login` to explicitly accept running without tracking, including in automation (a command without a terminal requires this flag). For automation, `--headless` skips the reminder and starts no new account session, even if you are signed in. See [headless commands](ci-integration.md#headless-commands). When running without tracking, no account session is created and no lab page is opened. Sign in optionally to track your time and results on astrona.io:

```sh
astrona login      # opens astrona.io in your browser; click Authorize there
astrona whoami     # who is signed in, and on which site (-o json: {signedIn, username, site, keyId})
astrona logout     # revoke the sign-in and forget it
```

`astrona login` signs in through the Astrona website, the same way you sign in on astrona.io — you never see any other sign-in page. It opens a page on astrona.io in your browser and shows a short code in the terminal:

```text
Opening your browser to sign in… If it doesn't open, go to https://astrona.io/cli/authorize?code=…
Confirm the code ABCD-EFGH matches the one in your browser.
Waiting for you to authorize this computer… (Ctrl+C cancels)
```

If you're already signed in on astrona.io, check that the code matches and click **Authorize**; if not, sign in on astrona.io first and you're brought straight back to that page. The terminal waits and finishes by itself. No password is ever typed into the terminal, and it works from a machine without a browser (open the printed link on any device). The computer shows up on your account under its hostname. Declining in the browser, or letting the code expire, leaves you signed out — run `astrona login` again.

The sign-in is kept in `~/.astrona/credentials.json` (mode 0600; astrona refuses to use it if other users can read it) and renewed automatically; `astrona logout` revokes it on astrona.io and deletes the file. Login and logout wait for any in-flight token renewal before replacing or deleting the saved sign-in, so a background reporter cannot restore the previous account. A revocation notice from an older website connection only removes the sign-in that connection used; it preserves a newer login. A credentials file from an older astrona (before website sign-in) no longer works: astrona treats you as signed out and asks you to run `astrona login` again.

Each login also makes a new device key for this computer and keeps its private half in `~/.astrona/device-key.json` (mode 0600, refused like the credentials file if other users can read it). Only the public half is sent to astrona.io. astrona signs every call to Astrona and every `astrona connect` with it, so a copied `credentials.json` is of no use on another machine. `astrona whoami` and `astrona check` show the key's fingerprint, the same one Account → Devices shows on astrona.io. `astrona logout` deletes the key with the sign-in. A sign-in from an astrona without device keys gets one the next time its token is renewed. If Astrona answers that a call's proof is missing or wrong, or `astrona connect` stops with "This computer's key does not match its sign-in", run `astrona login` again. A proof refused for its time means this computer's clock is off; astrona corrects for a small difference itself.

Updating the CLI keeps this login. Development builds made with `just build`
use a separate login in `~/.astrona-dev/credentials.json`; rebuilding also keeps
it. Switching between a development build and an installed release switches
profiles, so each needs its own login. The UI header shows the active profile
and site. `ASTRONA_URL` selects a different site without moving your saved login.

Then `astrona run ATS014/…`:

1. checks for an active sign-in **before** anything is fetched or built — signed out (or the sign-in expired), it runs locally without a session;
2. when signed in, asks Astrona for a lab session (too many open sessions or an unknown lab stop it here, with nothing built);
3. builds the lab;
4. when signed in, prints the lab page URL. A matching course page connects to the ready Lab without redirecting the browser, and the clock starts there, so setup time never counts. Open the printed URL when you are not already in the course. If the build fails, Astrona records that outcome. When Astrona sets a time limit for the attempt, run says so: `You have 60 minutes once the lab page opens.`

A module's playground has no lab page: its clock starts with the session, its timer (which removes it at the time limit) starts once it's ready, and if the build fails its clock is stopped again. The time limit is what astrona asked for (`metadata.timeLimit`): a deadline from the site outside it, or less than 5 minutes away, is moved into range.

A full `astrona reset ATS014/…` does the same: a fresh session and a new lab page (for a playground: a fresh clock and timer) once the lab is rebuilt. Starting over — `reset`, or `run` on a lab that's already running — first stops the old playground's clock and timer, so the old timer never removes the new lab. `reset --soft` and `reset --cluster` keep the current session and need no sign-in. `astrona run renew` skips (and forgets) a playground whose cluster no longer exists.

Only labs of the published trainings (repositories owned by the `astrona-io` GitHub organization) work this way. A lab from a source you added yourself (`astrona labs add`, or an organization set with `ASTRONA_CATALOG_ORG`) runs like a `--git` lab: no sign-in, no lab session, no results sent — astrona says so when it resolves the name — so another repository can never start a session in your account.

The session is remembered with the lab in `~/.astrona/labs/<lab>.json` (mode 0600: the site, the session id, the lab and its page — never a token). Then `astrona submit`:

1. reads the saved run's local files without checking GitHub or downloading the repository again, reuses approval for that source revision, and grades the lab;
2. sends the result — the same object `astrona submit -o json` prints — to the lab page, and prints `Sent to your lab page (attempt N): <url>`. Only a result graded from the lab the session was started for is sent (another config with the same `metadata.name` never reaches the page), and each check's message and hint are sent without control characters and cut at 2 KB;
3. ends the attempt, **PASS or FAIL**: the result carries the time you spent (`elapsedSeconds`), the attempt is marked passed or failed, and its timer stops. A submit is final — there is no fixing the lab and submitting again on the same attempt. A failed grade returns exit code 2, even when submission succeeds;
4. once the server confirms the result — or it is queued, below — automatically deletes the lab through the normal `astrona destroy` flow, including teardown scripts and kubectl context restoration. This also applies with `--headless`, `-o json`, and redirected input. There is no option to keep the lab: `teardown.keepCluster` does not apply to a submit. A local-only run (`--local-only`) is never tracked, so its result saved on this machine finishes it and deletes the lab too. If cleanup fails, submit exits 1, names the retry command, and preserves the recorded result. A run restarted during upload is never deleted.

Start a new attempt with `astrona run <lab>`. To check your work without ending the attempt, use `astrona submit --watch`: it grades locally without recording or sending results, finishing the attempt, stopping its timer or deleting the lab. Missing cached files must be restored before submission; submit does not download replacements for a saved run.

Each submit carries its own random key (`idempotency_key`). When Astrona can't be reached, answers with a server error, or needs a new sign-in, submit says the result is **queued**: it waits in the outbox (below) and goes out in the background with the same key, so a retry never records it twice — Astrona answers a key it already has with the stored result. A queued result still finishes the attempt and deletes the lab: the outbox sends the result before the lab's stop. A result for a session that already has signals waiting in the outbox goes behind them.

When Astrona refuses the result, the grade still stands and nothing is retried: an expired session (or one that isn't yours) says to run the lab again for a new attempt; an attempt that has already ended (a result was already submitted for it, pass or fail) says the attempt has ended and to start a new one with `astrona reset <lab>`; an attempt holding as many results as Astrona keeps shows Astrona's message plus `astrona reset <lab>` for a new tracked attempt; a session whose time ran out shows Astrona's message plus how to start a new attempt (`astrona reset <lab>`, or `astrona destroy <lab>` then `astrona run <lab>`). None of these deletes the lab, so `astrona reset <lab>` can rebuild it as a new attempt. If the run has a tracked session but you are signed out, the result waits in the outbox until you sign in again. An expired access token that cannot be renewed while offline also leaves the result queued. A run that never had a tracked session still needs a new run after login. Under `-o json` these lines go to stderr, so stdout stays exactly one JSON document.

`astrona destroy` of a lab that hasn't been submitted tells Astrona too: the attempt ends as *destroyed* — it stops showing as running on your labs page and never counts. Deletion sends only the session-closure reason, with no grading result or final usage summary. Unsubmitted work is discarded; a submitted attempt keeps its result, pass or fail. (`astrona reset` and a fresh `astrona run` close the old attempt the same way.)

Labs from your own files or repositories (`-c ./my-lab`, `--git <url>`) are not tied to an account: they run without signing in, and no page is opened.

Release builds default to `https://astrona.io`. Development builds (`just build`
or unversioned `go run`) default to `http://localhost:3000`. You can explicitly
select another site:

```sh
astrona login --site http://localhost:3000    # e.g. local development (plain http only to localhost)
```

The site is remembered with the sign-in: `whoami`, `logout` and catalog labs use it without repeating it. `ASTRONA_URL` overrides it for a single command (`ASTRONA_URL=https://staging.astrona.io astrona whoami`). The sign-in is only ever sent back to the site it came from: with `ASTRONA_URL` pointing elsewhere, you're signed out until you `astrona login` on that site. A site without astrona sign-in (`/api/cli/config` not found) makes `login` say so and suggest `--site`.

Signed in to any site other than astrona.io — a local or self-hosted Astrona — every command that sends it something says so first, so nothing goes to an unexpected place unnoticed:

```
[WARN] Not astrona.io — sending your lab results to http://localhost:3000
```

It is shown for the sign-in, a new lab session, `astrona submit`, `astrona run renew`, stopping a playground's clock and `astrona logout`; for astrona.io itself nothing extra is printed.

## Playgrounds

A module's playground (`astrona run ATS014/section-000/module-01/playground`) is a clean environment to try things in — nothing to submit, no lab page. Instead it is timed, and that time is recorded on your account as playground time:

1. Like a lab, `run` checks the sign-in and asks Astrona for a session **before** anything is built, and says how long you have: `Playground time started for <you> on <site> — 120 minutes until it stops and is removed.` The clock starts here.
2. The time limit is the playground's `metadata.timeLimit` in its `config.yaml` (`90m`, `2h`, `1h30m`; between 5 minutes and 24 hours — see the [lab config reference](../reference/lab-config.md#metadata)), else the site's default of 2 hours.
3. Once the playground is built, run starts a **watchdog** — a detached `astrona` process in its own session, so closing the terminal doesn't stop it — and prints when it ends: `Playground ready. It stops and is removed at 16:40 (in 2h0m0s)`. The watchdog compares against the wall clock every minute (a laptop that slept still stops on time); at the deadline it stops the clock on the site as "time limit" and removes the cluster exactly like `astrona destroy`. Its output goes to `~/.astrona/labs/<lab>.watchdog.log`. If it can't be started, run warns and the playground stays until you `astrona destroy` it — the site still stops the clock at the deadline.
4. `astrona destroy` ends it sooner: it stops the watchdog and the clock (as "destroyed") and removes the playground. It prints nothing extra when that works; if the site can't be reached it warns — the clock then stops by itself at the time limit.

Still working when the limit is near? `astrona run renew` starts the timer again with the full time limit, without touching the playground. Its session goes on, and all of its time counts as playground time when it ends:

```sh
astrona run renew                                              # every running playground
astrona run renew astrona.io/ATS014/section-000/module-01/playground
```

```text
Renewed astro-…: it now stops and is removed at 18:35 (in 2h0m0s).
```

A playground that already stopped can't be renewed — run it again. `astrona reset` of a playground does what `run` does: a fresh playground session with a new clock and watchdog once it is rebuilt. If a playground fails to build, its session is stopped, so no time is counted for it.

Before removing a playground, the watchdog locks it and rechecks the run,
session, and deadline. A watchdog left over from before a renew, or from a
replaced run, cannot stop the playground or remove the lab.

## Resource usage

While a signed-in lab or playground runs, `astrona run` also starts a **usage reporter** — another detached `astrona` process — that measures what the lab uses on your computer and sends it to the lab session every 15 seconds: CPU in cores and memory in bytes, for the kind cluster and its linked clusters (`docker stats` / `podman stats`) or the lab's QEMU VMs (`ps`). Nothing else is sent: no command output, no file contents, no process names. Astrona uses it to show what labs really cost to run, and what practising locally saves against the cloud.

The reporter ends on its own when the lab is destroyed or run again, when its session is gone or expired, or when the site stops taking samples (a lab that passed). It follows `astrona run renew`. Its output goes to `~/.astrona/labs/<lab>.usage.log`. `astrona submit` and playground deletion also send a short summary (average and peak CPU and memory), which the site uses when no sample reached it. A run that is not signed in sends nothing.

To send none of this, choose one of two ways:

- `astrona settings set usage-samples no` (or **usage-samples** in Preferences) turns it off for every run: no reporter starts, a running reporter stops at its next sample (within 15 seconds), samples still waiting in the outbox are dropped instead of sent, and `astrona submit` and playground deletion send no usage summary. Results already waiting in the outbox also omit their saved usage summary when delivered after you turn this setting off. Progress, results and the lab's clock are still sent. `astrona settings set usage-samples yes` turns it back on for the next `astrona run`.
- `astrona run --local-only <lab>` keeps one whole run on this machine: astrona does not read your sign-in, starts no lab session, and sends no progress, results, clock signals or usage. `astrona submit` grades it and records the result locally only, then deletes the lab like any finished run. Starting a running lab over with `--local-only` detaches its old session without closing it on Astrona.

`astrona logout` stops the usage reporters of running labs, since they post with your sign-in. The labs keep running; samples measured before the sign-out that were not sent yet wait in the outbox until you sign in again (or are dropped if usage samples are off). A playground's watchdog keeps running, so the playground is still removed at its time limit. Signing in again does not restart a reporter for a lab that is already running.

## Guest run reports

A catalog lab or playground run signed out ("continue as guest") sends one
report once it counts, so Astrona's hands-on time includes guests: a lab
when you submit it, a playground when it is destroyed. The report goes to
`{site}/api/cli/guest-runs` without any sign-in and holds only:

- an anonymous install id;
- the catalog lab id and the kind (`lab` or `playground`);
- the start and finish times and the time used (at most 8 hours);
- why it ended (`submitted` or `destroyed`), a one-time key and the CLI version.

Nothing else: no user name, e-mail, host name, command output or file
contents.

The install id is a random UUID v4 in `~/.astrona/install-id` (mode 0600),
made without asking the first time astrona needs it — the first guest lab or
playground start — and never derived from this computer. A successful
`astrona login` replaces it with a new random one, and so does
`astrona logout`, so guest runs before a sign-in, while signed in and after a
sign-out never share an id and can't be linked to your account. A report
already waiting keeps the id it was made with.

A report that can't be sent waits in `~/.astrona/guest-runs/` and is retried
by the background sender every `astrona` command starts; one still waiting
after 7 days is dropped. `--local-only`, `--headless`, `--git` and labs from
sources you added send nothing.

## Paused time and working offline

The browser workspace does not pause your time. Once you click **Start**, the lab's clock keeps running when you close the page or lose the connection, and a playground is still removed at its time limit.

Everything astrona tells Astrona about a running lab — the start from the browser workspace, pauses and resumes, resource samples, destroy and the time-limit stop — is saved on your computer first (`~/.astrona/outbox`) and then sent. If the connection drops, it waits there and goes out in order, with the time each thing happened, once Astrona is reachable again: from the usage reporter, or a background `astrona` that the next command starts. While you are signed out, signals wait for `astrona login`. Each lab session's signals go out in order; one Astrona can't accept yet holds back only that session's later signals, not other sessions'. A signal still waiting after 7 days is dropped, and a damaged outbox file is set aside (renamed `*.json.corrupt-*`, removed after 7 days) instead of blocking its session. A graded result (`astrona submit`) that could not be sent waits here too, with its key, so it is recorded once however often it is retried.

## Where the catalog comes from

- **Published trainings** — repositories in the [astrona-io](https://github.com/astrona-io) organization with the `astrona-training` topic. astrona reads them from astrona.io's lab catalog (`https://astrona.io/api/labs`), which Astrona syncs from those repositories, so it never runs into GitHub's API limits. A push to a training shows up once astrona.io has synced it (minutes). The catalog only names repositories: each is `https://github.com/astrona-io/<repo>.git`, its labs are fetched from GitHub with git and trust-checked as before. A development build with `ASTRONA_URL` set (a local stack) reads that site's catalog instead; a release build always reads astrona.io's.
- **Your own** — `astrona labs add <git url>` adds any git repository with an `astrona.yaml` (GitHub repos are read directly, others are cloned). `astrona labs remove <git url>` takes it out again, and `astrona labs sources` shows everything the catalog reads.

The catalog is cached for an hour in `~/.astrona/catalog.json`; `--refresh` reads it again. When it can't be refreshed (offline), the last copy is used, with a warning. `ASTRONA_CATALOG_ORG` points it at another organization, found with a GitHub search (unauthenticated, so GitHub's search limit applies); empty lists only your own sources.

## Lab names

A training's `astrona.yaml` lists its labs (`content` entries of `type: lab`, in `modules`, or in `sections` with their `modules` and `capstone`). A lab's name is the training id and the lab's path, without `sections/` and `labs/`:

```
sections/section-050/module-01/labs/lab-01   →   ATS014/section-050/module-01/lab-01
```

The training id (`training.id`, else the repository name) must be a plain name — letters, digits, `-` and `_` — and can't be an owner name (`astrona-io`, `astrona.io`): a name like `astrona.io/ATS014/…` always means the `astrona-io` repositories, never a training called `astrona.io`. A training with such an id is skipped, and listed among the catalog's errors.

Training ids are unique (in any case). When two sources declare the same id, the one from the `astrona-io` organization wins (else the first by repository URL); the other is skipped, and the catalog's errors name both repositories — so a source you add can't take over the name of a published training.

## Safety

A lab from the catalog is a remote lab: astrona fetches the training's repository with git and **asks for your trust** before running it, showing what it will do — like any `--git` lab (`--trust` approves it up front, e.g. in CI). An official training's lab (`astrona-io`) skips the prompt only when the fetched commit is the one astrona.io publishes for that training and the checkout is unchanged; any mismatch asks as usual (see [Official labs](remote-config.md#official-labs)). Everything a manifest says is treated as data: a lab path that would leave the repository is rejected. A path that exists on your disk always wins over a catalog name.

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
