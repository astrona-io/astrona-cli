// Package labops is the application layer: the lab operations that
// `astrona`'s commands run, without Cobra, prompts or printing of their own,
// so a browser workspace can call the same logic. Today it holds what
// astrona remembers about a running lab (where its config came from, the
// kubectl context it switched away from), the identity of a run, the
// lab's docs, the inventory of labs on this machine (`astrona list`), the
// signed-in account a lab runs under (AccountDeps, AccountForLab), the
// outbox of lab signals (queueing, the clock, the flusher behind `astrona
// outbox-flush`) and sending a graded result to the lab page.
//
// labops never writes to stdout or stderr itself: operations take an
// io.Writer or return results. Flags are read in cmd and passed in as
// parameters. Paths are computed inside functions, never at package init,
// so they follow apphome.Configure.
package labops
