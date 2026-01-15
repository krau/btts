package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// takeoutStage 表示导出阶段
type takeoutStage string

const (
	stageInit     takeoutStage = "init"
	stageDialogs  takeoutStage = "dialogs"
	stageExport   takeoutStage = "export"
	stageComplete takeoutStage = "complete"
)

// progressMsg 进度更新消息
type progressMsg struct {
	stage   takeoutStage
	current int
	total   int
	message string
}

// completeMsg 完成消息
type completeMsg struct{}

// errorMsg 错误消息
type errorMsg struct {
	err error
}

// takeoutProgressModel bubbletea model
type takeoutProgressModel struct {
	stage         takeoutStage
	spinner       spinner.Model
	progress      progress.Model
	current       int
	total         int
	message       string
	exportedChats int
	totalMessages int
	startTime     time.Time
	done          bool
	err           error
	width         int
}

// 样式定义
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)

	stageStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	messageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginLeft(2)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF0000"))

	statStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			MarginLeft(2)
)

func newTakeoutProgressModel() takeoutProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4"))

	return takeoutProgressModel{
		stage:     stageInit,
		spinner:   s,
		progress:  progress.New(progress.WithDefaultGradient()),
		startTime: time.Now(),
		width:     80,
	}
}

func (m takeoutProgressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tea.EnterAltScreen)
}

func (m takeoutProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.progress.Width = msg.Width - 4
		return m, nil

	case progressMsg:
		m.stage = msg.stage
		m.current = msg.current
		m.total = msg.total
		m.message = msg.message

		// 解析导出阶段的统计信息
		if msg.stage == stageComplete {
			// 从 message 中提取统计信息
			// 格式: "Completed: X messages from Y chats"
			var messages, chats int
			fmt.Sscanf(msg.message, "Completed: %d messages from %d chats", &messages, &chats)
			m.totalMessages = messages
			m.exportedChats = chats
		}

		return m, nil

	case completeMsg:
		m.done = true
		return m, tea.Quit

	case errorMsg:
		m.err = msg.err
		m.done = true
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd
	}

	return m, nil
}

func (m takeoutProgressModel) View() string {
	if m.done {
		if m.err != nil {
			return errorStyle.Render(fmt.Sprintf("✗ Export failed: %v\n", m.err))
		}

		elapsed := time.Since(m.startTime).Round(time.Second)
		result := strings.Builder{}
		result.WriteString(successStyle.Render("✓ Takeout export completed successfully!\n\n"))
		result.WriteString(statStyle.Render(fmt.Sprintf("  Total messages: %d\n", m.totalMessages)))
		result.WriteString(statStyle.Render(fmt.Sprintf("  Exported chats: %d\n", m.exportedChats)))
		result.WriteString(statStyle.Render(fmt.Sprintf("  Time elapsed: %s\n", elapsed)))
		return result.String()
	}

	var s strings.Builder

	// 标题
	s.WriteString(titleStyle.Render("Telegram Takeout Export"))
	s.WriteString("\n\n")

	// 当前阶段
	var stageText string
	switch m.stage {
	case stageInit:
		stageText = "Initializing Takeout session..."
	case stageDialogs:
		stageText = "Fetching dialogs"
	case stageExport:
		stageText = "Exporting messages"
	case stageComplete:
		stageText = "Completing export"
	}

	s.WriteString(m.spinner.View() + " ")
	s.WriteString(stageStyle.Render(stageText))
	s.WriteString("\n\n")

	// 进度条（仅在有进度时显示）
	if m.total > 0 {
		percent := float64(m.current) / float64(m.total)
		s.WriteString(m.progress.ViewAs(percent))
		fmt.Fprintf(&s, " %d/%d\n\n", m.current, m.total)
	}

	// 当前消息
	if m.message != "" {
		s.WriteString(messageStyle.Render(m.message))
		s.WriteString("\n")
	}

	// 时间统计
	elapsed := time.Since(m.startTime).Round(time.Second)
	s.WriteString("\n")
	s.WriteString(messageStyle.Render(fmt.Sprintf("Elapsed: %s", elapsed)))

	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render("Press Ctrl+C to cancel"))

	return s.String()
}

// runTakeoutWithProgress 运行 takeout 导出并显示进度
func runTakeoutWithProgress(exportFunc func(progressCallback func(stage string, current, total int, message string)) error) error {
	m := newTakeoutProgressModel()
	p := tea.NewProgram(m)

	// 在后台运行导出
	go func() {
		err := exportFunc(func(stage string, current, total int, message string) {
			p.Send(progressMsg{
				stage:   takeoutStage(stage),
				current: current,
				total:   total,
				message: message,
			})
		})

		if err != nil {
			p.Send(errorMsg{err: err})
		} else {
			p.Send(completeMsg{})
		}
	}()

	// 运行 bubbletea 程序
	if _, err := p.Run(); err != nil {
		return err
	}

	return nil
}
