package main

import (
	"fmt"
	"io"

	"astrona/internal/cluster"
)

func printLinkHints(w io.Writer, links []cluster.LinkState) {
	if len(links) == 0 {
		return
	}
	fmt.Fprintf(w, "\nLinked clusters (from pods: http://<name>.%s:<nodePort>):\n", cluster.LinkDomain)
	for _, l := range links {
		fmt.Fprintf(w, "    %-10s %s · kubectl --context %s · $%s_HOSTNAME\n", l.Name, l.Hostname(), l.Context(), l.EnvPrefix())
	}
}
