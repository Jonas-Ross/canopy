package tui

const (
	keyQuit    = 'q'
	keyDown    = 'j'
	keyUp      = 'k'
	keyRefresh = 'r'
	keyFilter  = '/'
	keyNew     = 'n'
	keyPrune   = 'd'
	keyOpenPR  = 'p'
	// keyKill is uppercase K because lowercase k is bound to up-nav.
	keyKill = 'K'
	// keyProcsToggle is uppercase P because lowercase p opens the PR.
	keyProcsToggle = 'P'
)

const filterPrompt = "filter: "

// footerKeys is the primary help footer rendered with bold/dim styling.
var footerKeys = []struct{ key, desc string }{
	{"j/k", "nav"},
	{"⏎", "shell"},
	{"n", "new"},
	{"d", "prune"},
	{"p", "PR"},
	{"P", "procs"},
	{"K", "kill"},
	{"r", "refresh"},
	{"/", "filter"},
	{"tab", "forensics"},
	{"q", "quit"},
}

const footerHelp = "j/k nav · ⏎ shell · n new · d prune · p PR · P procs · K kill · r refresh · / filter · q quit"
