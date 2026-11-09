// Package labops is the application layer: the lab operations that
// `astrona`'s commands run, without Cobra, prompts or printing of their own,
// so a browser workspace can call the same logic. Today it holds what
// astrona remembers about a running lab (where its config came from, the
// kubectl context it switched away from), the identity of a run, the
// lab's docs, the inventory of labs on this machine (`astrona list`), the
// signed-in account a lab runs under (AccountDeps, AccountForLab), the
// outbox of lab signals (queueing, the clock, the flusher behind `astrona
// outbox-flush` and starting it detached), sending a graded result to the
// lab page, starting a lab session, a timed playground (its detached
// watchdog behind `astrona playground-watchdog`, its clock, `astrona run
// renew`), the usage reporter behind `astrona usage-reporter`, loading a
// lab for a command (LoadLab: resolve, parse, the astronaVersion gate,
// warnings, bundle images) and the trust policy for remote labs
// (RequireTrust, HandoverApproval, RiskSummary).
//
// It also gathers `astrona status` (Status returns a StatusResult; cmd
// renders it as text or JSON) and builds the lab terminals cmd runs
// attached: the isolated kind shell (ShellCommand, through labshell) and
// the ssh login into a qemu VM (PrepareSSH, SSHSession.Command — isolated
// from the user's ssh config and agent, with the deprecated askpass
// password only on that ssh process's environment).
//
// Detached helpers are this same executable (os.Executable) started with
// a hidden command, in their own session (Setsid), inheriting the
// environment and logging next to the lab's state or the outbox.
//
// labops never writes to stdout or stderr itself: operations take an
// io.Writer or return results. Flags are read in cmd and passed in as
// parameters. What needs cmd is passed in as a callback: the version
// hand-over (VersionGate) and asking the user to trust a lab (Approver —
// the terminal prompt in cmd, later a browser question). Paths are computed inside functions, never at package init,
// so they follow apphome.Configure.
//
// Destroy, stop and start live here too: DestroyByName (under the lab's
// mutation lock; DestroyByNameLocked for a caller holding it),
// DestroyByPattern, DestroyByDiscovery, DestroyLab (a loaded Teardown,
// under the lock) and TearDownLabEnvironment, with outcomes written to
// DestroyOptions.Out; ResolveKindLab, PlanStop/Stop and Start (returning a
// StartResult) for `astrona stop`/`start`, which take no lab lock.
package labops
