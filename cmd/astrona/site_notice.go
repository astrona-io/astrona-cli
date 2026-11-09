package main

import (
	"os"

	"astrona/internal/labops"
)

// noticeOtherSite tells the user on stderr, right before a request that
// sends them data, when that request does not go to astrona.io
// (labops.NoticeOtherSite). what is what is being sent ("your lab results").
func noticeOtherSite(site, what string) { labops.NoticeOtherSite(os.Stderr, site, what) }
