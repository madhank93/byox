package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"

	"github.com/madhan/byox/course"
	"github.com/madhan/byox/diff"
	"github.com/madhan/byox/internal/progress"
	"github.com/madhan/byox/internal/runner"
)

type rightMode int

const (
	modeDesc rightMode = iota
	modeLog
	modeSolution
)

// byoxMarkdownStyle is glamour's dark theme with the literal "##"/"###"
// heading prefixes stripped — the built-in dark style prints them, which
// reads as leaked markdown syntax.
func byoxMarkdownStyle() glamour.TermRendererOption {
	cfg := styles.DarkStyleConfig
	cfg.H2.Prefix = ""
	cfg.H3.Prefix = ""
	cfg.H4.Prefix = ""
	cfg.H5.Prefix = ""
	cfg.H6.Prefix = ""
	return glamour.WithStyles(cfg)
}

var (
	topBarStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	sepStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cursorBg     = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	doneMark     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	currMark     = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	lockRow      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	passStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	failStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	runStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	barFill      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	barEmpty     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	filterHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stageTitleSt = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 1)

	// footer key hints
	keyCap      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("212")).Padding(0, 1)
	keyLabel    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	keyDot      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	filterInput = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))

	// top bar
	statsStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	streakStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
)

type courseItem struct {
	ref course.CourseRef
	def *course.Definition // nil until vendored
}

// row is one line of the grouped list: a course header or a stage.
type row struct {
	header bool
	course int // index into model.courses
	stage  int // stage index within the course (stage rows only)
}

type model struct {
	root string
	prog *progress.Store

	courses []courseItem
	folded  map[string]bool // by course slug

	rows     []row
	cursor   int
	offset   int
	filter   string
	typing   bool
	escArmed bool // previous key was a no-op esc (for double-esc fold)

	width, height int
	rightVP       viewport.Model
	spin          spinner.Model
	mode          rightMode

	descKey string
	rend    *glamour.TermRenderer
	rendW   int

	running   bool
	runCourse int // course index captured at startRun
	runPassed *bool
	runLog    strings.Builder
	runCh     chan tea.Msg

	watchCh     chan tea.Msg
	watcher     *fsnotify.Watcher
	watchedSlug string

	solutionFull bool // 's' view: diff against previous stage (false) or full file (true)

	err error
}

// Start launches the TUI.
func Start(root string, reg *course.Registry) error {
	prog, err := progress.Load(root)
	if err != nil {
		return err
	}
	m := &model{
		root:    root,
		prog:    prog,
		spin:    spinner.New(spinner.WithSpinner(spinner.Dot)),
		watchCh: make(chan tea.Msg, 8),
		folded:  map[string]bool{},
	}
	for _, ref := range reg.Entries {
		item := courseItem{ref: ref}
		if def, err := course.LoadDefinition(ref.VendorDir(root)); err == nil {
			item.def = def
		}
		m.courses = append(m.courses, item)
	}
	// Fold everything except the first usable course.
	first := true
	for _, c := range m.courses {
		if c.def != nil && first {
			first = false
			continue
		}
		m.folded[c.ref.Slug] = true
	}
	m.rebuildRows()
	m.jumpToCurrent(m.cursorCourse())
	m.rewatch()
	defer func() {
		if m.watcher != nil {
			m.watcher.Close()
		}
	}()
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m *model) Init() tea.Cmd { return listenWatch(m.watchCh) }

// --- rows / helpers ---

func (m *model) rebuildRows() {
	m.rows = m.rows[:0]
	f := strings.ToLower(m.filter)
	for ci, c := range m.courses {
		m.rows = append(m.rows, row{header: true, course: ci})
		if c.def == nil {
			continue
		}
		// While filtering, matching stages are always shown; otherwise
		// folded sections hide their stages.
		if f == "" && m.folded[c.ref.Slug] {
			continue
		}
		for si, s := range c.def.Stages {
			if f != "" &&
				!strings.Contains(strings.ToLower(s.Name), f) &&
				!strings.Contains(strings.ToLower(s.Slug), f) {
				continue
			}
			m.rows = append(m.rows, row{course: ci, stage: si})
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(len(m.rows)-1, 0)
	}
}

// cursorCourse returns the course index of the row under the cursor.
func (m *model) cursorCourse() int {
	if len(m.rows) == 0 {
		return 0
	}
	return m.rows[m.cursor].course
}

func (m *model) courseUsable(ci int) bool {
	return ci < len(m.courses) && m.courses[ci].def != nil
}

func (m *model) currentIdx(ci int) int {
	item := m.courses[ci]
	slugs := make([]string, len(item.def.Stages))
	for i, s := range item.def.Stages {
		slugs[i] = s.Slug
	}
	return m.prog.CurrentIndex(item.ref.Slug, slugs)
}

// jumpToCurrent puts the cursor on the current stage of course ci,
// unfolding it if needed.
func (m *model) jumpToCurrent(ci int) {
	if !m.courseUsable(ci) {
		return
	}
	delete(m.folded, m.courses[ci].ref.Slug)
	m.rebuildRows()
	cur := min(m.currentIdx(ci), len(m.courses[ci].def.Stages)-1)
	for ri, r := range m.rows {
		if !r.header && r.course == ci && r.stage == cur {
			m.cursor = ri
			break
		}
	}
	m.clampScroll()
}

func (m *model) rewatch() {
	ci := m.cursorCourse()
	if !m.courseUsable(ci) {
		return
	}
	slug := m.courses[ci].ref.Slug
	if slug == m.watchedSlug && m.watcher != nil {
		return
	}
	if m.watcher != nil {
		m.watcher.Close()
		m.watcher = nil
	}
	if w, err := startWatcher(m.courses[ci].ref.SolutionDir(m.root), m.watchCh); err == nil {
		m.watcher = w
		m.watchedSlug = slug
	}
}

func (m *model) listHeight() int {
	// total - top bar(1) - footer(1)
	return max(m.height-2, 3)
}

func (m *model) leftWidth() int {
	return max(min(m.width/2, 64), 34)
}

func (m *model) rightWidth() int {
	return max(m.width-m.leftWidth()-3, 20)
}

func (m *model) clampScroll() {
	m.cursor = min(max(m.cursor, 0), max(len(m.rows)-1, 0))
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = min(max(m.offset, 0), max(len(m.rows)-h, 0))
}

// --- right pane ---

func (m *model) renderer() *glamour.TermRenderer {
	w := m.rightWidth() - 2
	if m.rend == nil || m.rendW != w {
		m.rend, _ = glamour.NewTermRenderer(
			byoxMarkdownStyle(),
			glamour.WithWordWrap(max(w, 20)),
		)
		m.rendW = w
	}
	return m.rend
}

func (m *model) ensureViewport() {
	// Right block = 1 title line + viewport; total layout is
	// top bar(1) + block(height-2) + footer(1).
	w, h := m.rightWidth(), max(m.height-3, 5)
	if m.rightVP.Width != w || m.rightVP.Height != h {
		m.rightVP = viewport.New(max(w, 20), h)
		m.descKey = ""
	}
}

func (m *model) showDesc() {
	m.mode = modeDesc
	m.solutionFull = false
	if len(m.rows) == 0 {
		m.rightVP.SetContent(helpStyle.Render("run `just setup` first"))
		m.descKey = ""
		return
	}
	r := m.rows[m.cursor]
	item := m.courses[r.course]

	var key, md string
	if r.header {
		key = fmt.Sprintf("course/%s/%d", item.ref.Slug, m.rightWidth())
		if key == m.descKey {
			return
		}
		if item.def == nil {
			md = fmt.Sprintf("# %s\n\n_Not set up — run `just setup`._", item.ref.Name)
		} else {
			done, total := m.currentIdx(r.course), len(item.def.Stages)
			next := "course complete 🎉"
			if done < total {
				next = fmt.Sprintf("next: **%s** (`%s`)", item.def.Stages[done].Name, item.def.Stages[done].Slug)
			}
			md = fmt.Sprintf("# %s\n\n%d/%d stages complete — %s\n\n%s",
				item.ref.Name, done, total, next, item.def.DescriptionMD)
		}
	} else {
		stage := item.def.Stages[r.stage]
		key = fmt.Sprintf("stage/%s/%s/%d", item.ref.Slug, stage.Slug, m.rightWidth())
		if key == m.descKey {
			return
		}
		md = item.def.StageDescription(item.ref.VendorDir(m.root), stage)
	}
	rendered, err := m.renderer().Render(md)
	if err != nil {
		rendered = md
	}
	m.rightVP.SetContent(rendered)
	m.rightVP.GotoTop()
	m.descKey = key
}

// solutionFile returns the path to the vendored reference solution's
// main .go for a stage, and whether one exists. Reference solutions ship
// only for the free preview stages; the rest are gated on CodeCrafters.
func (m *model) solutionFile(ci, stageIdx int) (string, bool) {
	item := m.courses[ci]
	slug := item.def.Stages[stageIdx].Slug
	nn := fmt.Sprintf("%02d-%s", stageIdx+1, slug)

	// 1) Authored snapshots maintained in-repo take precedence.
	authored := filepath.Join(m.root, "reference-solutions", item.ref.Slug, nn, "main.go")
	if _, err := os.Stat(authored); err == nil {
		return authored, true
	}

	// 2) Vendored CodeCrafters reference (free preview stages only).
	dir := filepath.Join(item.ref.VendorDir(m.root), "solutions", "go", nn, "code", "app")
	main := filepath.Join(dir, "main.go")
	if _, err := os.Stat(main); err == nil {
		return main, true
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".go") {
				return filepath.Join(dir, e.Name()), true
			}
		}
	}
	return "", false
}

// previousSolutionFile returns the solution file for the nearest earlier
// stage in the same course that has one. Reference solutions are
// cumulative — each stage's main.go is the full program through that
// stage — so diffing against the previous stage's file is what isolates
// "the solution for this stage" rather than everything before it too.
func (m *model) previousSolutionFile(ci, stageIdx int) (string, bool) {
	for idx := stageIdx - 1; idx >= 0; idx-- {
		if p, ok := m.solutionFile(ci, idx); ok {
			return p, true
		}
	}
	return "", false
}

// showSolution renders the reference solution for the cursor's stage into
// the right pane. By default it shows only what changed since the
// previous stage's solution (the reference files are cumulative full
// programs, not per-stage diffs); `f` toggles to the full file.
func (m *model) showSolution() {
	m.mode = modeSolution
	m.descKey = ""
	if len(m.rows) == 0 || m.rows[m.cursor].header {
		m.rightVP.SetContent(m.renderMD("_Move to a stage row to see its reference solution._"))
		return
	}
	r := m.rows[m.cursor]
	path, ok := m.solutionFile(r.course, r.stage)
	if !ok {
		m.rightVP.SetContent(m.renderMD(
			"### No reference solution vendored\n\n" +
				"CodeCrafters ships reference solutions only for the free preview " +
				"stages; the rest require a logged-in account. This stage isn't one " +
				"of the bundled ones.\n\nPress `esc` to return to the instructions."))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.rightVP.SetContent(m.renderMD("error reading solution: " + err.Error()))
		return
	}
	content := string(data)
	base := filepath.Base(path)

	if m.solutionFull {
		md := "### Reference solution — full file\n\n`" + base + "`\n\n```go\n" + content + "\n```"
		m.rightVP.SetContent(m.renderMD(md))
		m.rightVP.GotoTop()
		return
	}

	prevPath, prevOK := m.previousSolutionFile(r.course, r.stage)
	if !prevOK {
		md := "### Reference solution\n\n`" + base + "`  ·  first vendored stage for this course\n\n```go\n" + content + "\n```"
		m.rightVP.SetContent(m.renderMD(md))
		m.rightVP.GotoTop()
		return
	}
	prevData, err := os.ReadFile(prevPath)
	if err != nil {
		md := "### Reference solution\n\n`" + base + "`\n\n```go\n" + content + "\n```"
		m.rightVP.SetContent(m.renderMD(md))
		m.rightVP.GotoTop()
		return
	}
	delta, changed := diff.Unified(string(prevData), content)
	if !changed {
		md := "### Reference solution\n\nNo code changes for this stage — `" + base +
			"` is identical to the previous stage's solution. This stage's work is in " +
			"the tests/behavior, not new code.\n\nPress `f` to see the full file anyway."
		m.rightVP.SetContent(m.renderMD(md))
		m.rightVP.GotoTop()
		return
	}
	md := "### Reference solution — changes for this stage\n\n`" + base + "`\n\n```diff\n" + delta + "\n```"
	m.rightVP.SetContent(m.renderMD(md))
	m.rightVP.GotoTop()
}

func (m *model) renderMD(md string) string {
	out, err := m.renderer().Render(md)
	if err != nil {
		return md
	}
	return out
}

// --- editor handoff ---

type editorDoneMsg struct{ err error }

// openEditor suspends the TUI and launches $EDITOR (or $VISUAL, else vi)
// on the cursor course's solution main.go.
func (m *model) openEditor() tea.Cmd {
	ci := m.cursorCourse()
	if !m.courseUsable(ci) {
		return nil
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	file := filepath.Join(m.courses[ci].ref.SolutionDir(m.root), "app", "main.go")
	// editor may carry flags, e.g. "code --wait".
	fields := strings.Fields(editor)
	args := append(fields[1:], file)
	c := exec.Command(fields[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

// --- test-run plumbing ---

type logLineMsg string
type runDoneMsg struct{ err error }

func (m *model) startRun() tea.Cmd {
	ci := m.cursorCourse()
	if !m.courseUsable(ci) || m.running {
		return nil
	}
	ch := make(chan tea.Msg, 64)
	m.runCh = ch
	m.running = true
	m.runCourse = ci
	m.runPassed = nil
	m.runLog.Reset()
	m.mode = modeLog
	m.ensureViewport()
	m.rightVP.SetContent("")
	m.descKey = ""

	item := m.courses[ci]
	idx := m.currentIdx(ci)
	if idx >= len(item.def.Stages) {
		idx = len(item.def.Stages) - 1 // course done: rerun everything
	}
	pr, pw := io.Pipe()
	go func() {
		err := runner.Run(item.ref, m.root, item.def.Stages, idx, pw)
		pw.Close()
		ch <- runDoneMsg{err: err}
	}()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			ch <- logLineMsg(sc.Text())
		}
	}()
	return tea.Batch(m.spin.Tick, listen(ch))
}

func listen(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// --- update ---

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Degenerate ptys (CI, expect) can report 0×0 — fall back to 80×24.
		m.width, m.height = msg.Width, msg.Height
		if m.width <= 0 {
			m.width = 80
		}
		if m.height <= 0 {
			m.height = 24
		}
		m.ensureViewport()
		if m.mode == modeLog {
			m.rightVP.SetContent(m.runLog.String())
			m.rightVP.GotoBottom()
		} else {
			m.showDesc()
		}
		m.clampScroll()
		return m, nil

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case fileChangedMsg:
		var cmd tea.Cmd
		if !m.running {
			cmd = m.startRun()
		}
		return m, tea.Batch(listenWatch(m.watchCh), cmd)

	case logLineMsg:
		m.runLog.WriteString(string(msg) + "\n")
		if m.mode == modeLog {
			m.rightVP.SetContent(m.runLog.String())
			m.rightVP.GotoBottom()
		}
		return m, listen(m.runCh)

	case runDoneMsg:
		m.running = false
		passed := msg.err == nil
		m.runPassed = &passed
		if m.courseUsable(m.runCourse) {
			item := m.courses[m.runCourse]
			m.prog.RecordRun(item.ref.Slug, passed)
			if passed {
				idx := m.currentIdx(m.runCourse)
				if idx < len(item.def.Stages) {
					m.prog.MarkCompleted(item.ref.Slug, item.def.Stages[idx].Slug)
				}
			}
			if err := m.prog.Save(); err != nil {
				m.err = err
			}
			if passed {
				m.jumpToCurrent(m.runCourse)
			}
		}
		return m, nil

	case editorDoneMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("editor: %w", msg.err)
		}
		// Saved code may change results; rerun unless already running.
		var cmd tea.Cmd
		if !m.running {
			cmd = m.startRun()
		}
		return m, cmd

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.rightVP, cmd = m.rightVP.Update(msg)
	return m, cmd
}

// handleMouse routes the scroll wheel: over the left pane it moves the
// stage-list cursor; over the right pane it scrolls the description.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	up := msg.Button == tea.MouseButtonWheelUp
	down := msg.Button == tea.MouseButtonWheelDown
	if !up && !down {
		return m, nil
	}
	if msg.X < m.leftWidth() && !m.typing { // over the list
		if up {
			m.cursor--
		} else {
			m.cursor++
		}
		m.clampScroll()
		m.afterMove()
		return m, nil
	}
	if up { // over the description / log pane
		m.rightVP.LineUp(3)
	} else {
		m.rightVP.LineDown(3)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.typing {
		switch key {
		case "enter":
			m.typing = false
		case "esc":
			m.typing = false
			m.filter = ""
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.filter += string(msg.Runes)
			}
		}
		m.cursor, m.offset = 0, 0
		m.rebuildRows()
		m.clampScroll()
		m.afterMove()
		return m, nil
	}

	if key != "esc" {
		m.escArmed = false
	}

	switch key {
	case "q":
		if !m.running {
			return m, tea.Quit
		}
	case "up", "k":
		m.cursor--
		m.clampScroll()
		m.afterMove()
	case "down", "j":
		m.cursor++
		m.clampScroll()
		m.afterMove()
	case "g", "home":
		m.cursor = 0
		m.clampScroll()
		m.afterMove()
	case "G", "end":
		m.cursor = len(m.rows) - 1
		m.clampScroll()
		m.afterMove()
	case "c":
		m.filter = ""
		m.jumpToCurrent(m.cursorCourse())
		m.afterMove()
	case "enter", "h", "l", "left", "right":
		if len(m.rows) > 0 && m.rows[m.cursor].header && m.filter == "" {
			m.toggleFold(m.cursorCourse())
		}
	case "/":
		m.typing = true
	case "t":
		if !m.running {
			return m, m.startRun()
		}
	case "e":
		if !m.running {
			return m, m.openEditor()
		}
	case "s":
		if m.mode == modeSolution {
			m.descKey = ""
			m.showDesc()
		} else if !m.running {
			m.showSolution()
		}
	case "f":
		if m.mode == modeSolution {
			m.solutionFull = !m.solutionFull
			m.showSolution()
		}
	case "esc":
		switch {
		case (m.mode == modeLog || m.mode == modeSolution) && !m.running:
			m.descKey = ""
			m.showDesc()
		case m.filter != "":
			m.filter = ""
			m.rebuildRows()
			m.jumpToCurrent(m.cursorCourse())
			m.afterMove()
		case m.escArmed:
			// Second esc with nothing to dismiss: fold/unfold the
			// cursor's course from anywhere, no trip to the header.
			m.escArmed = false
			m.toggleFold(m.cursorCourse())
		default:
			m.escArmed = true
		}
		return m, nil
	case "pgup", "K", "shift+up":
		m.rightVP.HalfPageUp()
	case "pgdown", "J", "shift+down":
		m.rightVP.HalfPageDown()
	}
	return m, nil
}

// toggleFold folds/unfolds course ci and parks the cursor on its header
// so the result is visible regardless of where the cursor started.
func (m *model) toggleFold(ci int) {
	if m.filter != "" || !m.courseUsable(ci) {
		return
	}
	slug := m.courses[ci].ref.Slug
	if m.folded[slug] {
		delete(m.folded, slug)
	} else {
		m.folded[slug] = true
	}
	m.rebuildRows()
	for ri, r := range m.rows {
		if r.header && r.course == ci {
			m.cursor = ri
			break
		}
	}
	m.clampScroll()
	m.afterMove()
}

// afterMove refreshes the description pane and the file watcher after
// any cursor movement. Navigating away from a finished test log (or a
// solution view) returns to the stage instructions.
func (m *model) afterMove() {
	if (m.mode == modeLog || m.mode == modeSolution) && !m.running {
		m.mode = modeDesc
		m.descKey = ""
	}
	if m.mode == modeDesc {
		m.showDesc()
	}
	if !m.running {
		m.rewatch()
	}
}

// --- view ---

func (m *model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	var b strings.Builder
	b.WriteString(m.topBar() + "\n")
	left := m.listView()
	right := m.rightView()
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, sepView(max(m.height-2, 1)), right))
	b.WriteString("\n" + m.footerView())
	return b.String()
}

// topBar shows the cursor's course with a full-width progress bar.
func (m *model) topBar() string {
	ci := m.cursorCourse()
	if !m.courseUsable(ci) {
		return topBarStyle.Render(" byox ")
	}
	item := m.courses[ci]
	done, total := m.currentIdx(ci), len(item.def.Stages)
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}

	label := topBarStyle.Render(item.ref.Slug) + " "
	stats := fmt.Sprintf(" %d/%d (%d%%)", done, total, pct)
	streak := fmt.Sprintf("  🔥 streak %d ", m.prog.Streak(item.ref.Slug))
	right := statsStyle.Render(stats) + streakStyle.Render(streak)

	bw := max(m.width-lipgloss.Width(label)-lipgloss.Width(stats)-lipgloss.Width(streak)-1, 10)
	filled := 0
	if total > 0 {
		filled = done * bw / total
	}
	return " " + label + gradientBar(filled, bw) + right
}

// gradientBar renders a solid progress bar with a blue→purple gradient
// across the filled cells (golings style).
func gradientBar(filled, width int) string {
	const lo, hi = 63, 141 // 256-color blue → purple
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			c := lo
			if filled > 1 {
				c = lo + (hi-lo)*i/(filled-1)
			}
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("%d", c))).Render("█"))
		} else {
			b.WriteString(barEmpty.Render("█"))
		}
	}
	return b.String()
}

func sepView(h int) string {
	col := make([]string, h)
	for i := range col {
		col[i] = sepStyle.Render(" │ ")
	}
	return strings.Join(col, "\n")
}

// maxStages returns the widest stage count across courses, for a
// uniform number column.
func (m *model) maxStages() int {
	n := 1
	for _, c := range m.courses {
		if c.def != nil {
			n = max(n, len(c.def.Stages))
		}
	}
	return n
}

func (m *model) listView() string {
	lw := m.leftWidth()
	if len(m.rows) == 0 {
		return lipgloss.NewStyle().Width(lw).Render("\n  run `just setup` first")
	}
	h := m.listHeight()

	numW := len(fmt.Sprintf("%d", m.maxStages()))
	nameW := max(lw-4-numW-2, 10)

	var lines []string
	for ri := m.offset; ri < min(m.offset+h, len(m.rows)); ri++ {
		r := m.rows[ri]
		item := m.courses[r.course]
		var line string

		if r.header {
			arrow := "▾"
			if m.folded[item.ref.Slug] && m.filter == "" {
				arrow = "▸"
			}
			name := item.ref.Name
			var right string
			if item.def != nil {
				done, total := m.currentIdx(r.course), len(item.def.Stages)
				right = m.miniBar(done, total, 8) + fmt.Sprintf(" %d/%d", done, total)
			} else {
				right = lockRow.Render("not set up")
			}
			pad := max(lw-2-lipgloss.Width(arrow+" "+name)-lipgloss.Width(right)-1, 1)
			line = " " + headerStyle.Render(arrow+" "+name) + strings.Repeat(" ", pad) + right
		} else {
			s := item.def.Stages[r.stage]
			cur := m.currentIdx(r.course)

			var mark string
			switch {
			case r.stage < cur:
				mark = doneMark.Render("✓")
			case r.stage == cur:
				mark = currMark.Render("▶")
			default:
				mark = lockRow.Render("○")
			}
			name := s.Name
			if lipgloss.Width(name) > nameW {
				name = truncate(name, nameW-1) + "…"
			}
			name = fmt.Sprintf("%-*s", nameW, name)
			num := fmt.Sprintf("%*d", numW, r.stage+1)
			if r.stage > cur { // locked: dim the whole row
				line = fmt.Sprintf("   %s %s %s", mark, num, lockRow.Render(name))
			} else {
				line = fmt.Sprintf("   %s %s %s", mark, num, name)
			}
		}

		if ri == m.cursor {
			line = cursorBg.Render("▸" + line[1:])
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(lw).Render(line))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(lw).Render(strings.Join(lines, "\n"))
}

func (m *model) miniBar(done, total, bw int) string {
	filled := 0
	if total > 0 {
		filled = done * bw / total
	}
	return barFill.Render(strings.Repeat("▰", filled)) + barEmpty.Render(strings.Repeat("▱", bw-filled))
}

// truncate cuts a plain (unstyled) string to width cells.
func truncate(s string, w int) string {
	out := ""
	for _, r := range s {
		if lipgloss.Width(out+string(r)) > w {
			break
		}
		out += string(r)
	}
	return out
}

func (m *model) rightView() string {
	rw := m.rightWidth()
	title := ""
	switch {
	case m.running:
		title = runStyle.Render(m.spin.View() + "running tests…")
	case m.runPassed != nil && m.mode == modeLog:
		if *m.runPassed {
			title = passStyle.Render("PASS ✓ stage complete — next stage unlocked")
		} else {
			title = failStyle.Render("FAIL ✗ fix and save to retest (esc → instructions)")
		}
	case m.mode == modeSolution && m.solutionFull:
		title = runStyle.Render("⚠ reference solution · full file — esc/s back · f changes only")
	case m.mode == modeSolution:
		title = runStyle.Render("⚠ reference solution · changes for this stage — esc/s back · f full file")
	case len(m.rows) > 0:
		r := m.rows[m.cursor]
		item := m.courses[r.course]
		if r.header {
			title = stageTitleSt.Render(item.ref.Name)
		} else {
			s := item.def.Stages[r.stage]
			title = stageTitleSt.Render(fmt.Sprintf("stage %d · %s", r.stage+1, s.Name))
		}
	}
	title = lipgloss.NewStyle().MaxWidth(rw).Render(title)
	return lipgloss.NewStyle().Width(rw).Render(title + "\n" + m.rightVP.View())
}

// keyHint renders a "key label" pair with an accented key.
func keyHint(key, label string) string {
	return keyCap.Render(key) + " " + keyLabel.Render(label)
}

func hintLine(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = keyHint(p[0], p[1])
	}
	return " " + strings.Join(parts, keyDot.Render(" · "))
}

func (m *model) footerView() string {
	if m.typing {
		return " " + keyCap.Render("/") + filterInput.Render(m.filter+"█") +
			keyDot.Render("  ") + keyLabel.Render("enter keep · esc clear")
	}
	if m.err != nil {
		return failStyle.Render(" error: " + m.err.Error())
	}
	if m.filter != "" {
		return " " + keyLabel.Render("filter:") + filterInput.Render(m.filter) +
			keyDot.Render("   ") + hintLine([2]string{"esc", "clear"}, [2]string{"t", "test"}, [2]string{"q", "quit"})[1:]
	}
	return hintLine(
		[2]string{"↑↓", "move"},
		[2]string{"enter", "fold"},
		[2]string{"t", "test"},
		[2]string{"e", "edit"},
		[2]string{"s", "solution"},
		[2]string{"/", "filter"},
		[2]string{"c", "current"},
		[2]string{"J/K", "scroll"},
		[2]string{"q", "quit"},
	)
}
