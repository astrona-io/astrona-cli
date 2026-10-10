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

Catalog labs are tied to your Astrona account, so the lab page on astrona.io can track your time. Sign in once per computer:

```sh
astrona login      # opens astrona.io in your browser; click Authorize there
astrona whoami     # who is signed in, and on which site
astrona logout     # revoke the sign-in and forget it
```

`astrona login` signs in through the Astrona website, the same way you sign in on astrona.io — you never see any other sign-in page. It opens a page on astrona.io in your browser and shows a short code in the terminal:

```text
Opening your browser to sign in… If it doesn't open, go to https://astrona.io/cli/authorize?code=…
Confirm the code ABCD-EFGH matches the one in your browser.
Waiting for you to authorize this computer… (Ctrl+C cancels)
```

If you're already signed in on astrona.io, check that the code matches and click **Authorize**; if not, sign in on astrona.io first and you're brought straight back to that page. The terminal waits and finishes by itself. No password is ever typed into the terminal, and it works from a machine without a browser (open the printed link on any device). The computer shows up on your account under its hostname. Declining in the browser, or letting the code expire, leaves you signed out — run `astrona login` again.

The sign-in is kept in `~/.astrona/credentials.json` (mode 0600; astrona refuses to use it if other users can read it) and renewed automatically; `astrona logout` revokes it on astrona.io and deletes the file. A credentials file from an older astrona (before website sign-in) no longer works: astrona treats you as signed out and asks you to run `astrona login` again.

Then `astrona run ATS014/…`:

1. checks the sign-in **before** anything is fetched or built — signed out (or the sign-in expired), it stops and tells you to run `astrona login`;
2. asks Astrona for a lab session (too many open sessions or an unknown lab stop it here, with nothing built);
3. builds the lab;
4. prints the lab page URL and opens it in your browser — the clock starts there, so setup time never counts. Without a browser (a headless box), open the printed URL yourself. If the build fails, nothing is opened. When Astrona sets a time limit for the attempt, run says so: `You have 60 minutes once the lab page opens.`

A full `astrona reset ATS014/…` does the same: a fresh session and a new lab page once the lab is rebuilt. `reset --soft` and `reset --cluster` keep the current session and need no sign-in.

Only labs of the published trainings (repositories owned by the `astrona-io` GitHub organization) work this way. A lab from a source you added yourself (`astrona labs add`, or an organization set with `ASTRONA_CATALOG_ORG`) runs like a `--git` lab: no sign-in, no lab session, no results sent — astrona says so when it resolves the name — so another repository can never start a session in your account.

The session is remembered with the lab in `~/.astrona/labs/<lab>.json` (mode 0600: the site, the session id, the lab and its page — never a token). Then `astrona submit`:

1. grades the lab exactly as always (the Proctor decides; the exit code is the grade's);
2. sends the result — the same object `astrona submit -o json` prints — to the lab page, and prints `Sent to your lab page (attempt N): <url>`;
3. after a **pass** that was sent, asks `Delete the lab cluster now? [Y/n]` — Enter deletes it exactly like `astrona destroy` (your kubectl context comes back too); `n` keeps it. `--keep`, `-o json` or no terminal keep it and print how to remove it later. A failing result never asks: the clock keeps running, so fix it and submit again.

When the result can't be sent, the grade still stands: an expired session (or one that isn't yours) says to run the lab again for a new attempt, an attempt that's already finished shows Astrona's message, a session whose time ran out shows Astrona's message plus how to start a new attempt (`astrona reset <lab>`, or `astrona destroy <lab>` then `astrona run <lab>`) and never offers to delete the lab, and a network or server error is a warning. Signed out, submit says to `astrona login` and run the lab again. Under `-o json` these lines go to stderr, so stdout stays exactly one JSON document.

Labs from your own files or repositories (`-c ./my-lab`, `--git <url>`) are not tied to an account: they run without signing in, and no page is opened.

The site is `https://astrona.io` unless you sign in to another one:

```sh
astrona login --site http://localhost:3000    # e.g. local development (plain http only to localhost)
```

The site is remembered with the sign-in: `whoami`, `logout` and catalog labs use it without repeating it. `ASTRONA_URL` overrides it for a single command (`ASTRONA_URL=https://staging.astrona.io astrona whoami`). The sign-in is only ever sent back to the site it came from: with `ASTRONA_URL` pointing elsewhere, you're signed out until you `astrona login` on that site. A site without astrona sign-in (`/api/cli/config` not found) makes `login` say so and suggest `--site`.

Signed in to any site other than astrona.io — a local or self-hosted Astrona — every command that sends it something says so first, so nothing goes to an unexpected place unnoticed:

```
[WARN] Not astrona.io — sending your lab results to http://localhost:3000
```

It is shown for the sign-in, a new lab session, `astrona submit`, `astrona run renew`, stopping a playground's clock and `astrona logout`; for astrona.io itself nothing extra is printed.

## Where the catalog comes from

- **Published trainings** — repositories in the [astrona-io](https://github.com/astrona-io) organization with the `astrona-training` topic. astrona finds them with one GitHub search.
- **Your own** — `astrona labs add <git url>` adds any git repository with an `astrona.yaml` (GitHub repos are read directly, others are cloned). `astrona labs remove <git url>` takes it out again, and `astrona labs sources` shows everything the catalog reads.

The catalog is cached for an hour in `~/.astrona/catalog.json`; `--refresh` reads it again. When it can't be refreshed (offline), the last copy is used, with a warning. `ASTRONA_CATALOG_ORG` points it at another organization (empty: only your own sources).

## Lab names

A training's `astrona.yaml` lists its labs (`content` entries of `type: lab`, in `modules`, or in `sections` with their `modules` and `capstone`). A lab's name is the training id and the lab's path, without `sections/` and `labs/`:

```
sections/section-050/module-01/labs/lab-01   →   ATS014/section-050/module-01/lab-01
```

The training id (`training.id`, else the repository name) must be a plain name — letters, digits, `-` and `_` — and can't be an owner name (`astrona-io`, `astrona.io`): a name like `astrona.io/ATS014/…` always means the `astrona-io` repositories, never a training called `astrona.io`. A training with such an id is skipped, and listed among the catalog's errors.

Training ids are unique (in any case). When two sources declare the same id, the one from the `astrona-io` organization wins (else the first by repository URL); the other is skipped, and the catalog's errors name both repositories — so a source you add can't take over the name of a published training.

## Safety

A lab from the catalog is a remote lab: astrona fetches the training's repository with git and **asks for your trust** before running it, showing what it will do — like any `--git` lab (`--trust` approves it up front, e.g. in CI). Everything a manifest says is treated as data: a lab path that would leave the repository is rejected. A path that exists on your disk always wins over a catalog name.

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
