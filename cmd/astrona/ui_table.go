package main

// Reserve space for status even when a long lab name needs truncating.
func uiRunColumns(name, kind, status, age string, width int) string {
	column := func(text string, size int) string {
		return workspaceFit(sanitizeLine(text), size, 1)
	}
	if width < 70 {
		return column(name, max(1, width-18)) + "  " + column(status, 16)
	}
	return column(name, width-41) + "  " + column(kind, 10) + "  " + column(status, 16) + "  " + column(age, 9)
}
