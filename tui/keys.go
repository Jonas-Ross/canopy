package tui

const (
	keyQuit      = 'q'
	keyDown      = 'j'
	keyUp        = 'k'
	keyRefresh   = 'r'
	keyFilter    = '/'
	keyForensics = 'f'
)

const (
	footerForensics = "v2: forensics — coming soon"
	footerTab       = "tab: v2 analytical tab unavailable"
	filterPrompt    = "filter: "
)

// footerKeys is the default help footer — rendered with bold/dim styling in view.go.
var footerKeys = []struct{ key, desc string }{
	{"j/k", "nav"},
	{"r", "refresh"},
	{"/", "filter"},
	{"q", "quit"},
}

// footerHelp is the plain-text fallback used when styled rendering is bypassed
// (e.g. on key-triggered transient footer messages).
const footerHelp = "j/k nav · r refresh · / filter · q quit"
