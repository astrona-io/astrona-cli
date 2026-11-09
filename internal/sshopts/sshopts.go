// Package sshopts holds the ssh client options every astrona connection
// into a lab VM uses, so the student terminal and the CLI's own admin
// automation get the same isolation from the host.
package sshopts

import "strconv"

// Isolated disables ssh client features that execute commands on the host
// or expose it to the VM: the user's ssh config (-F /dev/null, so no
// ProxyCommand, LocalCommand, ControlMaster or extra identities), the
// escape character, agent and X11 forwarding, and any port forwarding.
func Isolated() []string {
	return []string{
		"-F", "/dev/null",
		"-o", "EscapeChar=none",
		"-o", "PermitLocalCommand=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
	}
}

// Unattended is Isolated plus what a connection nobody types into needs:
// only the given -i key is offered, ssh never prompts, and connecting gives
// up after connectTimeout seconds.
func Unattended(connectTimeout int) []string {
	return append(Isolated(),
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout="+strconv.Itoa(connectTimeout),
	)
}
