package main

import (
	"astrona/internal/account"
	"astrona/internal/ui"
)

// noticeOtherSite tells the user, right before a request that sends them data,
// when that request does not go to astrona.io: the CLI is open source and can
// sign in to any Astrona — a local one while developing, or a self-hosted one.
// Silent for astrona.io. what is what is being sent ("your lab results").
func noticeOtherSite(site, what string) {
	if site == "" || account.IsDefaultSite(site) {
		return
	}
	ui.Warnf("Not astrona.io — sending %s to %s", what, site)
}
