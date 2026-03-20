package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rarnen-tech/go-torrent-cli/internal/torrent"
)

const (
	defaultWidth = 96
	minWidth     = 72
)

type model struct {
	client    *torrent.Client
	input     textinput.Model
	selected  int
	inputMode bool
	mode      string
	width     int
	status    string
	statusErr bool
}

type tickMsg struct{}

type eventMsg struct {
	event torrent.Event
}

var (
	greenBright = lipgloss.Color("#72F08C")
	greenSoft   = lipgloss.Color("#B8F4C5")
	greenMute   = lipgloss.Color("#8DDBA1")
	greenDim    = lipgloss.Color("#3F8752")
	redSoft     = lipgloss.Color("#F48F7A")
)

var banner = []string{
	" _____                           _   ",
	"|_   _|__  _ __ _ __ ___ _ __  _| |_ ",
	"  | |/ _ \\| '_/| '_// _ \\ '_ \\|__  _|",
	"  | | |_| | |  | | |  __/ | | | | |",
	"  |_|\\___/|_|  |_|  \\___|_| |_| \\_|",
}

func NewModel(client *torrent.Client) model {
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 64
	input.Prompt = ""
	input.Placeholder = "C:\\...\\movie.torrent or magnet:?xt=..."

	return model{
		client: client,
		input:  input,
		width:  defaultWidth,
		status: "Ready.",
	}
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func waitEvent(ch <-chan torrent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return eventMsg{}
		}
		return eventMsg{event: event}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), waitEvent(m.client.Events))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width - 4
		if m.width < minWidth {
			m.width = minWidth
		}
		m.input.Width = maxInt(24, m.width-14)
		return m, nil

	case tickMsg:
		return m, tick()

	case eventMsg:
		m.applyEvent(msg.event)
		return m, waitEvent(m.client.Events)

	case tea.KeyMsg:
		if m.inputMode {
			return m.handleInputKeys(msg)
		}
		return m.handleMainKeys(msg)
	}

	return m, nil
}

func (m model) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.submitInput()
		return m, nil
	case "esc":
		m.resetInput()
		m.setStatus("Input cancelled.", false)
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleMainKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ids, _ := snapshotDownloads(m.client)

	switch msg.String() {
	case "i", "\u0448":
		return m.enterInputMode("download")
	case "p", "\u0437":
		return m.enterInputMode("path")
	case "up":
		if m.selected > 0 {
			m.selected--
		}
	case "down":
		if m.selected < len(ids)-1 {
			m.selected++
		}
	case "s", "\u044b":
		if id := selectedID(ids, m.selected); id != "" {
			m.client.StopDownload(id)
			m.setStatus("Stopped task "+id+".", false)
		}
	case "r", "\u043a":
		if id := selectedID(ids, m.selected); id != "" {
			m.client.ResumeDownload(id)
			m.setStatus("Resumed task "+id+".", false)
		}
	case "d", "\u0432", "delete":
		if id := selectedID(ids, m.selected); id != "" {
			m.client.DeleteDownload(id)
			if m.selected > 0 && m.selected >= len(ids)-1 {
				m.selected--
			}
			m.setStatus("Deleted task "+id+".", false)
		}
	case "esc", "q", "\u0439", "ctrl+c":
		return m, tea.Quit
	}

	return m, nil
}

func (m model) enterInputMode(mode string) (tea.Model, tea.Cmd) {
	m.inputMode = true
	m.mode = mode
	m.input.SetValue("")

	if mode == "path" {
		m.input.Placeholder = "C:\\...\\Desktop"
	} else {
		m.input.Placeholder = "C:\\...\\movie.torrent or magnet:?xt=..."
	}

	return m, m.input.Focus()
}

func (m *model) submitInput() {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		m.setStatus("Input is empty.", true)
		m.resetInput()
		return
	}

	if m.mode == "path" {
		m.client.SetDownloadPath(value)
		m.setStatus("Save path updated.", false)
		m.resetInput()
		return
	}

	clean := strings.Trim(strings.TrimSpace(value), "\"'`")
	lower := strings.ToLower(clean)

	switch {
	case strings.HasPrefix(lower, "magnet:"):
		id := m.client.DownloadMagnet(clean)
		if id == "" {
			m.setStatus("Could not add magnet.", true)
		} else {
			m.setStatus("Queued task "+id+".", false)
		}
	case strings.HasSuffix(lower, ".torrent") || strings.HasPrefix(lower, "file:///"):
		id := m.client.DownloadTorrentFile(clean)
		if id == "" {
			m.setStatus("Could not add torrent file.", true)
		} else {
			m.setStatus("Queued task "+id+".", false)
		}
	default:
		m.setStatus("Input must be a magnet link or .torrent path.", true)
	}

	m.resetInput()
}

func (m *model) resetInput() {
	m.inputMode = false
	m.mode = ""
	m.input.Blur()
	m.input.SetValue("")
}

func (m *model) applyEvent(event torrent.Event) {
	if event.Type == "" && strings.TrimSpace(event.Message) == "" {
		return
	}

	line := formatEventLine(event)
	if line == "" {
		return
	}

	switch event.Type {
	case torrent.EventError:
		m.setStatus(line, true)
	case torrent.EventDownloadStarted, torrent.EventDownloadFinished:
		m.setStatus(line, false)
	case torrent.EventDownloadProgress:
		lower := strings.ToLower(line)
		if strings.Contains(lower, "refreshing trackers") || strings.Contains(lower, "fetching metadata") {
			m.setStatus(line, false)
		}
	}
}

func (m *model) setStatus(line string, isErr bool) {
	m.status = strings.TrimSpace(line)
	if m.status == "" {
		m.status = "Ready."
	}
	m.statusErr = isErr
}

func formatEventLine(event torrent.Event) string {
	line := cleanText(event.Message)
	if line == "" {
		return ""
	}

	if event.TaskID != "" && !strings.Contains(line, "id:"+event.TaskID) {
		return "Task " + event.TaskID + ": " + line
	}

	return line
}

func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = defaultWidth
	}
	if width < minWidth {
		width = minWidth
	}

	ids, downloads := snapshotDownloads(m.client)
	selected := clampIndex(m.selected, len(ids))
	innerWidth := maxInt(60, width-10)

	lines := []string{
		m.renderBanner(width),
		panelBox("Downloads", m.renderList(innerWidth, ids, downloads, selected), width),
		panelBox("Selected", m.renderDetails(innerWidth, ids, downloads, selected), width),
		panelBox(
			"Controls",
			"Save to : "+trimEnd(m.client.GetDownloadPath(), innerWidth-10)+"\n"+
				m.renderInput(innerWidth)+"\n"+
				m.renderStatus(innerWidth)+"\n"+
				lipgloss.NewStyle().Foreground(greenDim).Render("I add  P path  Up/Down move  S stop  R resume  D delete  Esc quit"),
			width,
		),
	}

	return lipgloss.NewStyle().
		Foreground(greenSoft).
		Padding(1, 2).
		Render(strings.TrimSpace(strings.Join(lines, "\n\n")))
}

func (m model) renderBanner(width int) string {
	lines := make([]string, 0, len(banner))
	style := lipgloss.NewStyle().Foreground(greenBright).Bold(true)
	for _, line := range banner {
		lines = append(lines, style.Render(line))
	}

	subtitle := lipgloss.NewStyle().
		Foreground(greenMute).
		Render("simple green client")

	return lipgloss.NewStyle().
		Width(maxInt(24, width-4)).
		Render(strings.Join(lines, "\n") + "\n" + subtitle)
}

func (m model) renderList(width int, ids []string, downloads map[string]*torrent.DownloadTask, selected int) string {
	lines := []string{
		lipgloss.NewStyle().Foreground(greenBright).Bold(true).Render(m.headerRow(width)),
		lipgloss.NewStyle().Foreground(greenDim).Render(tableRule(width)),
	}

	if len(ids) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(greenDim).Render("No torrents yet. Press I to add one."))
		return strings.Join(lines, "\n")
	}

	for i, id := range ids {
		task := downloads[id]
		if task == nil {
			continue
		}

		row := m.taskRow(width, id, task, i == selected)
		style := lipgloss.NewStyle().Foreground(greenSoft)
		if i == selected {
			style = lipgloss.NewStyle().
				Foreground(greenBright).
				Background(lipgloss.Color("#143520")).
				Bold(true)
		}
		if task.Status == "error" {
			style = lipgloss.NewStyle().
				Foreground(redSoft).
				Background(lipgloss.Color("#2A120F")).
				Bold(true)
		}
		lines = append(lines, style.Render(row))
	}

	return strings.Join(lines, "\n")
}

func (m model) headerRow(width int) string {
	idWidth, stateWidth, etaWidth, speedWidth, progressWidth, nameWidth := tableWidths(width)
	return fmt.Sprintf(
		"%s | %s | %s | %s | %s | %s",
		fitCell("ID", idWidth),
		fitCell("STATE", stateWidth),
		fitCell("ETA", etaWidth),
		fitCell("SPEED", speedWidth),
		fitCell("PROGRESS", progressWidth),
		fitCell("NAME", nameWidth),
	)
}

func (m model) taskRow(width int, id string, task *torrent.DownloadTask, selected bool) string {
	idWidth, stateWidth, etaWidth, speedWidth, progressWidth, nameWidth := tableWidths(width)
	progress := fitCell(progressCell(progressPercent(task), progressWidth), progressWidth)
	name := fitCellEnd(trimEnd(taskTitle(task), nameWidth), nameWidth)

	return fmt.Sprintf(
		"%s | %s | %s | %s | %s | %s",
		fitCellEnd(id, idWidth),
		fitCellEnd(taskStateLabel(task), stateWidth),
		fitCellEnd(etaLabel(task), etaWidth),
		fitCellEnd(speedLabel(task.Speed), speedWidth),
		progress,
		name,
	)
}

func (m model) renderDetails(width int, ids []string, downloads map[string]*torrent.DownloadTask, selected int) string {
	if len(ids) == 0 || selected >= len(ids) {
		return lipgloss.NewStyle().Foreground(greenDim).Render("No torrent selected.")
	}

	task := downloads[ids[selected]]
	if task == nil {
		return lipgloss.NewStyle().Foreground(greenDim).Render("No torrent selected.")
	}

	lines := []string{
		"Selected: " + trimEnd(taskTitle(task), width-10),
		"Source  : " + trimEnd(cleanText(task.Source), width-10),
		fmt.Sprintf("Peers   : %d    ETA: %s    Speed: %s", task.Peers, etaLabel(task), speedLabel(task.Speed)),
		"Progress: " + renderProgressBar(progressPercent(task), maxInt(12, minInt(22, width-30))),
		"Data    : " + transferLabel(task),
		"Status  : " + trimEnd(taskStatusNote(task), width-10),
	}

	if task.LastError != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(redSoft).Render("Error   : "+trimEnd(cleanText(task.LastError), width-10)))
	}

	return strings.Join(lines, "\n")
}

func (m model) renderInput(width int) string {
	label := "Add     : "
	if m.mode == "path" {
		label = "Set path: "
	}

	value := m.input.View()
	if !m.inputMode {
		value = lipgloss.NewStyle().Foreground(greenDim).Render(m.input.Placeholder)
	}

	line := label + value
	if lipgloss.Width(line) > width {
		line = label + trimEnd(strings.TrimSpace(m.input.Value()), width-len(label))
		if !m.inputMode && strings.TrimSpace(m.input.Value()) == "" {
			line = label + trimEnd(m.input.Placeholder, width-len(label))
		}
	}

	if m.inputMode {
		modeLabel := "Mode    : add torrent"
		if m.mode == "path" {
			modeLabel = "Mode    : change save path"
		}
		return modeLabel + "\n" + line
	}

	return line
}

func (m model) renderStatus(width int) string {
	style := lipgloss.NewStyle().Foreground(greenMute)
	if m.statusErr {
		style = lipgloss.NewStyle().Foreground(redSoft)
	}
	return style.Render("Status  : " + trimEnd(cleanText(m.status), width-18))
}

func taskTitle(task *torrent.DownloadTask) string {
	if task == nil {
		return "unknown"
	}
	if strings.TrimSpace(task.Name) != "" {
		return cleanText(task.Name)
	}
	if base := filepath.Base(task.Source); base != "" && base != "." {
		return cleanText(base)
	}
	return cleanText(task.Source)
}

func taskStateLabel(task *torrent.DownloadTask) string {
	if task == nil {
		return "unknown"
	}

	switch task.Status {
	case "finished":
		return "done"
	case "error":
		return "error"
	case "stopped":
		return "stopped"
	case "queued":
		return "queued"
	case "waiting":
		if task.Peers > 0 {
			return "peers"
		}
		return "waiting"
	case "metadata":
		return "meta"
	case "connecting":
		if task.Peers > 0 {
			return "handshake"
		}
		return "connect"
	case "downloading":
		if task.Progress >= 100 {
			return "done"
		}
		if task.Speed > 0 {
			return "active"
		}
		if task.Peers > 0 {
			return "peers"
		}
		return "waiting"
	default:
		return cleanText(task.Status)
	}
}

func taskStatusNote(task *torrent.DownloadTask) string {
	if task == nil {
		return "No task."
	}

	switch task.Status {
	case "finished":
		return "Download completed."
	case "error":
		if task.LastError != "" {
			return task.LastError
		}
		return "Download failed."
	case "stopped":
		return "Task is stopped."
	case "queued":
		return "Waiting to start."
	case "waiting":
		if task.Peers > 0 {
			return "Peers are here. Looking for data."
		}
		return "Waiting for peers."
	case "metadata":
		return "Reading torrent metadata."
	case "connecting":
		return "Opening peer connections."
	case "downloading":
		if task.Progress >= 100 {
			return "Download completed."
		}
		if task.Speed > 0 {
			return "Downloading data."
		}
		if task.Peers > 0 {
			return "Peers are here. Looking for data."
		}
		return "Waiting for peers."
	default:
		return cleanText(task.Status)
	}
}

func etaLabel(task *torrent.DownloadTask) string {
	if task == nil {
		return "n/a"
	}
	switch task.Status {
	case "stopped", "queued", "error":
		return "--"
	case "waiting":
		return "--"
	}
	if task.Progress >= 100 {
		return "done"
	}
	if task.ETA <= 0 {
		if task.Status == "metadata" {
			return "metadata"
		}
		return "--"
	}

	d := time.Duration(task.ETA) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func renderProgressBar(progress float64, width int) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	if width < 6 {
		width = 6
	}

	filled := int(progress * float64(width) / 100)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("=", filled) + strings.Repeat(".", width-filled)
}

func progressPercent(task *torrent.DownloadTask) float64 {
	if task == nil || task.TotalBytes <= 0 {
		return float64(task.Progress)
	}

	percent := float64(task.CompletedBytes) * 100 / float64(task.TotalBytes)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func progressCell(percent float64, width int) string {
	percentText := fmt.Sprintf("%3.0f%%", percent)
	if percent > 0 && percent < 10 {
		percentText = fmt.Sprintf("%3.1f%%", percent)
	}

	barWidth := width - lipgloss.Width(percentText) - 1
	if barWidth < 6 {
		barWidth = 6
	}

	return percentText + " " + renderProgressBar(percent, barWidth)
}

func transferLabel(task *torrent.DownloadTask) string {
	if task == nil {
		return "0 B"
	}
	if task.TotalBytes <= 0 {
		return humanBytes(task.CompletedBytes)
	}
	return humanBytes(task.CompletedBytes) + " / " + humanBytes(task.TotalBytes)
}

func humanBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}

	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}

	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func speedLabel(speed float64) string {
	if speed <= 0 {
		return "--"
	}
	return fmt.Sprintf("%.1f MB/s", speed)
}

func snapshotDownloads(client *torrent.Client) ([]string, map[string]*torrent.DownloadTask) {
	downloads := client.GetDownloads()
	return torrent.SortedIDs(downloads), downloads
}

func selectedID(ids []string, index int) string {
	if index < 0 || index >= len(ids) {
		return ""
	}
	return ids[index]
}

func clampIndex(index int, size int) int {
	if size <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= size {
		return size - 1
	}
	return index
}

func fitCell(value string, width int) string {
	value = trimMiddle(strings.TrimSpace(value), width)
	padding := width - lipgloss.Width(value)
	if padding < 0 {
		padding = 0
	}
	return value + strings.Repeat(" ", padding)
}

func fitCellEnd(value string, width int) string {
	value = trimEnd(strings.TrimSpace(value), width)
	padding := width - lipgloss.Width(value)
	if padding < 0 {
		padding = 0
	}
	return value + strings.Repeat(" ", padding)
}

func trimMiddle(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || lipgloss.Width(value) <= max {
		return value
	}
	if max <= 3 {
		runes := []rune(value)
		if len(runes) > max {
			runes = runes[:max]
		}
		return string(runes)
	}

	runes := []rune(value)
	if len(runes) <= max {
		return value
	}

	head := (max - 3) / 2
	tail := max - head - 3
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail >= len(runes) {
		return string(runes[:max])
	}

	return string(runes[:head]) + "..." + string(runes[len(runes)-tail:])
}

func trimEnd(value string, max int) string {
	value = cleanText(value)
	if max <= 0 || lipgloss.Width(value) <= max {
		return value
	}
	if max <= 3 {
		runes := []rune(value)
		if len(runes) > max {
			runes = runes[:max]
		}
		return string(runes)
	}

	runes := []rune(value)
	if len(runes) <= max {
		return value
	}

	return string(runes[:max-3]) + "..."
}

func cleanText(value string) string {
	// cut bad text
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if idx := strings.Index(value, "\\x00"); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.IndexRune(value, '\x00'); idx >= 0 {
		value = value[:idx]
	}

	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, value)

	return strings.Join(strings.Fields(value), " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func panelBox(title string, body string, width int) string {
	contentWidth := maxInt(24, width-4)
	header := ""
	if strings.TrimSpace(title) != "" {
		header = lipgloss.NewStyle().
			Foreground(greenBright).
			Bold(true).
			Render(" " + title + " ")
	}

	return lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(greenDim).
		Padding(0, 1).
		Render(strings.TrimSpace(header + "\n" + body))
}

func tableWidths(width int) (int, int, int, int, int, int) {
	idWidth := 4
	stateWidth := 10
	etaWidth := 7
	speedWidth := 8
	progressWidth := 16
	separators := 5 * 3
	nameWidth := width - (idWidth + stateWidth + etaWidth + speedWidth + progressWidth + separators)
	if nameWidth < 14 {
		progressWidth -= 2
		nameWidth = width - (idWidth + stateWidth + etaWidth + speedWidth + progressWidth + separators)
	}
	if nameWidth < 12 {
		stateWidth -= 1
		speedWidth -= 1
		nameWidth = width - (idWidth + stateWidth + etaWidth + speedWidth + progressWidth + separators)
	}
	if nameWidth < 10 {
		nameWidth = 10
	}
	return idWidth, stateWidth, etaWidth, speedWidth, progressWidth, nameWidth
}

func tableRule(width int) string {
	idWidth, stateWidth, etaWidth, speedWidth, progressWidth, nameWidth := tableWidths(width)
	return strings.Repeat("-", idWidth) + "-+-" +
		strings.Repeat("-", stateWidth) + "-+-" +
		strings.Repeat("-", etaWidth) + "-+-" +
		strings.Repeat("-", speedWidth) + "-+-" +
		strings.Repeat("-", progressWidth) + "-+-" +
		strings.Repeat("-", nameWidth)
}

func RunTUI(client *torrent.Client) {
	program := tea.NewProgram(
		NewModel(client),
		tea.WithAltScreen(),
	)

	if err := program.Start(); err != nil {
		fmt.Println("Error:", err)
	}
}
