package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type engine struct {
	config       *Config
	env          environmentInfo
	colorWriter  colorWriter
	ansi         *ansiUtils
	consoleTitle *consoleTitle

	console strings.Builder
	rprompt string
}

func (e *engine) write(text string) {
	e.console.WriteString(text)
}

func (e *engine) string() string {
	return e.console.String()
}

func (e *engine) backspace(length int) {
	text := e.ansi.truncateBy(e.string(), length)
	e.console.Reset()
	e.write(text)
}

func (e *engine) canWriteRPrompt() bool {
	prompt := e.string()
	consoleWidth, err := e.env.getTerminalWidth()
	if err != nil || consoleWidth == 0 {
		return true
	}
	promptWidth := e.ansi.lenWithoutANSI(prompt)
	availableSpace := consoleWidth - promptWidth
	// spanning multiple lines
	if availableSpace < 0 {
		overflow := promptWidth % consoleWidth
		availableSpace = consoleWidth - overflow
	}
	promptBreathingRoom := 30
	canWrite := (availableSpace - e.ansi.lenWithoutANSI(e.rprompt)) >= promptBreathingRoom
	return canWrite
}

func (e *engine) render() string {
	lineLength := 0
	for i, block := range e.config.Blocks {

		if i != 0 {
			block.previousActiveBlock = e.config.Blocks[i-1]
		}

		// TODO: Remove finding future block lengths
		maxLength := -1
		if block.Type == Connection {
			// futureBlockLengths := 0
			// if len(e.config.Blocks) > i+1 {
			// 	for _, futureBlock := range e.config.Blocks[i+1:] {
			// 		cleanFutureBlock := futureBlock
			// 		if cleanFutureBlock.Newline {
			// 			break
			// 		} else {
			// 			futureBlockLengths += e.getBlockLength(cleanFutureBlock)
			// 		}
			// 	}
			// }

			consoleWidth := 0
			switch e.env.getPlatform() {
			case windowsPlatform:

				command := exec.Command("mode", "con")
				buffer := new(bytes.Buffer)
				command.Stdout = buffer
				command.Run() // TODO: Check for error
				scanner := bufio.NewScanner(buffer)
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), "Columns") {
						consoleWidth, _ = strconv.Atoi(strings.Replace(scanner.Text(), "    Columns:        ", "", 1))
						break
					}
				}
			case linuxPlatform:
				// TODO: Add support for connection block types on Linux
			}

			// maxLength = consoleWidth - lineLength - futureBlockLengths + 3
			maxLength = consoleWidth - lineLength + 3
		} else {
			lineLength += e.getBlockLength(block)
		}
		e.renderBlock(block, maxLength)
	}
	if e.config.ConsoleTitle {
		e.write(e.consoleTitle.getConsoleTitle())
	}
	e.write(e.ansi.creset)
	if e.config.FinalSpace {
		e.write(" ")
	}

	if !e.config.OSC99 {
		return e.print()
	}
	cwd := e.env.getcwd()
	if e.env.isWsl() {
		cwd, _ = e.env.runCommand("wslpath", "-m", cwd)
	}
	e.write(e.ansi.consolePwd(cwd))
	return e.print()
}

func (e *engine) getBlockLength(block *Block) int {
	block.init(e.env, e.colorWriter, e.ansi)
	block.setStringValues()

	blockText := ""
	if block.Newline {
		return 0
	}
	switch block.Type {
	// This is deprecated but leave if to not break current configs
	// It is encouraged to used "newline": true on block level
	// rather than the standalone the linebreak block
	case LineBreak:
		return 0
	case Prompt:
		blockText = block.renderSegments()
	case RPrompt:
		blockText = block.renderSegments()
	case Connection:
		return 0
	}

	return e.ansi.lenWithoutANSI(blockText)
}

func (e *engine) renderBlock(block *Block, maxLength int) {
	// when in bash, for rprompt blocks we need to write plain
	// and wrap in escaped mode or the prompt will not render correctly
	if block.Type == RPrompt && e.env.getShellName() == bash {
		block.initPlain(e.env, e.config)
	} else {
		block.init(e.env, e.colorWriter, e.ansi)
	}
	block.setStringValues()
	if !block.enabled() {
		return
	}
	if block.Newline {
		e.write("\n")
	}
	switch block.Type {
	// This is deprecated but leave if to not break current configs
	// It is encouraged to used "newline": true on block level
	// rather than the standalone the linebreak block
	case LineBreak:
		e.write("\n")
	case Prompt:
		if block.VerticalOffset != 0 {
			e.write(e.ansi.changeLine(block.VerticalOffset))
		}
		switch block.Alignment {
		case Right:
			blockText := block.renderSegments()
			if block.previousActiveBlock.Type == Connection {
				e.backspace(e.ansi.lenWithoutANSI(blockText))
			}
			e.write(e.ansi.carriageForward())
			e.write(e.ansi.getCursorForRightWrite(blockText, block.HorizontalOffset))
			if maxLength == -1 {
				e.write(blockText)
			} else if maxLength > 1 {
				e.write(blockText[:maxLength-1])
			}

		case Left:
			blockText := block.renderSegments()
			if maxLength == -1 {
				e.write(blockText)
			} else if maxLength > 1 {
				e.write(blockText[:maxLength-1])
			}
		}
	case RPrompt:
		blockText := block.renderSegments()
		if e.env.getShellName() == bash {
			blockText = fmt.Sprintf(e.ansi.bashFormat, blockText)
		}
		e.rprompt = blockText
	case Connection:
		blockText := block.renderSegments()
		blockText = strings.Repeat(blockText, (maxLength-e.ansi.lenWithoutANSI(blockText))/e.ansi.lenWithoutANSI(blockText))

		if maxLength == -1 {
			e.write(blockText)
		} else if maxLength > 1 {
			e.write(e.ansi.truncateTo(blockText, maxLength))
		}
	}

	// Due to a bug in Powershell, the end of the line needs to be cleared.
	// If this doesn't happen, the portion after the prompt gets colored in the background
	// color of the line above the new input line. Clearing the line fixes this,
	// but can hopefully one day be removed when this is resolved natively.
	if e.ansi.shell == pwsh || e.ansi.shell == powershell5 {
		e.write(e.ansi.clearAfter())
	}
}

// debug will loop through your config file and output the timings for each segments
func (e *engine) debug() string {
	var segmentTimings []*SegmentTiming
	largestSegmentNameLength := 0
	e.write("\n\x1b[1mHere are the timings of segments in your prompt:\x1b[0m\n\n")

	// console title timing
	start := time.Now()
	consoleTitle := e.consoleTitle.getTemplateText()
	duration := time.Since(start)
	segmentTiming := &SegmentTiming{
		name:            "ConsoleTitle",
		nameLength:      12,
		enabled:         e.config.ConsoleTitle,
		stringValue:     consoleTitle,
		enabledDuration: 0,
		stringDuration:  duration,
	}
	segmentTimings = append(segmentTimings, segmentTiming)
	// loop each segments of each blocks
	for _, block := range e.config.Blocks {
		block.init(e.env, e.colorWriter, e.ansi)
		longestSegmentName, timings := block.debug()
		segmentTimings = append(segmentTimings, timings...)
		if longestSegmentName > largestSegmentNameLength {
			largestSegmentNameLength = longestSegmentName
		}
	}

	// pad the output so the tabs render correctly
	largestSegmentNameLength += 7
	for _, segment := range segmentTimings {
		duration := segment.enabledDuration.Milliseconds()
		if segment.enabled {
			duration += segment.stringDuration.Milliseconds()
		}
		segmentName := fmt.Sprintf("%s(%t)", segment.name, segment.enabled)
		e.write(fmt.Sprintf("%-*s - %3d ms - %s\n", largestSegmentNameLength, segmentName, duration, segment.stringValue))
	}
	return e.string()
}

func (e *engine) print() string {
	switch e.env.getShellName() {
	case zsh:
		if !*e.env.getArgs().Eval {
			break
		}
		// escape double quotes contained in the prompt
		prompt := fmt.Sprintf("PS1=\"%s\"", strings.ReplaceAll(e.string(), "\"", "\"\""))
		prompt += fmt.Sprintf("\nRPROMPT=\"%s\"", e.rprompt)
		return prompt
	case pwsh, powershell5, bash, plain:
		if e.rprompt == "" || !e.canWriteRPrompt() {
			break
		}
		e.write(e.ansi.saveCursorPosition)
		e.write(e.ansi.carriageForward())
		e.write(e.ansi.getCursorForRightWrite(e.rprompt, 0))
		e.write(e.rprompt)
		e.write(e.ansi.restoreCursorPosition)
	}
	return e.string()
}

func (e *engine) renderTooltip(tip string) string {
	tip = strings.Trim(tip, " ")
	var tooltip *Segment
	for _, tp := range e.config.Tooltips {
		if !tp.shouldInvokeWithTip(tip) {
			continue
		}
		tooltip = tp
	}
	if tooltip == nil {
		return ""
	}
	if err := tooltip.mapSegmentWithWriter(e.env); err != nil {
		return ""
	}
	if !tooltip.enabled() {
		return ""
	}
	tooltip.stringValue = tooltip.string()
	// little hack to reuse the current logic
	block := &Block{
		Alignment: Right,
		Segments:  []*Segment{tooltip},
	}
	switch e.env.getShellName() {
	case zsh:
		block.init(e.env, e.colorWriter, e.ansi)
		return block.renderSegments()
	case pwsh, powershell5:
		block.initPlain(e.env, e.config)
		tooltipText := block.renderSegments()
		e.write(e.ansi.clearAfter())
		e.write(e.ansi.carriageForward())
		e.write(e.ansi.getCursorForRightWrite(tooltipText, 0))
		e.write(tooltipText)
		return e.string()
	}
	return ""
}

func (e *engine) renderTransientPrompt() string {
	if e.config.TransientPrompt == nil {
		return ""
	}
	promptTemplate := e.config.TransientPrompt.Template
	if len(promptTemplate) == 0 {
		promptTemplate = "{{ .Shell }}> "
	}
	template := &textTemplate{
		Template: promptTemplate,
		Env:      e.env,
	}
	prompt := template.renderPlainContextTemplate(nil)
	e.colorWriter.write(e.config.TransientPrompt.Background, e.config.TransientPrompt.Foreground, prompt)
	switch e.env.getShellName() {
	case zsh:
		// escape double quotes contained in the prompt
		prompt := fmt.Sprintf("PS1=\"%s\"", strings.ReplaceAll(e.colorWriter.string(), "\"", "\"\""))
		prompt += "\nRPROMPT=\"\""
		return prompt
	case pwsh, powershell5:
		return e.colorWriter.string()
	}
	return ""
}
