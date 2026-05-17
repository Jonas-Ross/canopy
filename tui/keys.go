package tui

// Key constants used in Model.Update. Kept in one place so the footer
// help text and the switch cases always agree.
const (
	keyQuit      = 'q'
	keyDown      = 'j'
	keyUp        = 'k'
	keyRefresh   = 'r'
	keyFilter    = '/'
	keyForensics = 'f'
)

// footerHelp is the default footer shown when not filtering.
const footerHelp = "j/k navigate  r refresh  / filter  tab v2  q quit"

// footerForensics is shown after pressing f.
const footerForensics = "v2: forensics — coming soon"

// footerTab is shown after pressing tab.
const footerTab = "tab: v2 analytical tab unavailable"

// filterPrompt is the prompt shown in filter mode.
const filterPrompt = "filter: "
