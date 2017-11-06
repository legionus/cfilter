/* cfilter.go
 *
 * This file is part of cfilter
 * Copyright (C) 2017  Alexey Gladkov <gladkov.alexey@gmail.com>
 *
 * This file is covered by the GNU General Public License,
 * which should be included with cfilter as the file COPYING.
 */
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/legionus/getopt"
)

var (
	prog       = ""
	version    = "1.0"
	bufferSize = 4096
)

type Group struct {
	Name     string
	Number   int
	Colorize Colorize
}

type Pattern struct {
	RE     *regexp.Regexp
	Groups []Group
}

type LinePositionKind int

const (
	LinePositionStartKind LinePositionKind = 0
	LinePositionEndKind   LinePositionKind = 1
)

type LinePosition struct {
	Kind     LinePositionKind
	Order    int
	Offset   int
	Colorize Colorize
}

type LinePositions []*LinePosition

func (a LinePositions) Len() int           { return len(a) }
func (a LinePositions) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a LinePositions) Less(i, j int) bool { return a[i].Offset < a[j].Offset }

func showUsage(*getopt.Option, getopt.NameType, string) error {
	fmt.Fprintf(os.Stdout, `
Usage: %[1]s [options] [FILE...]
   or: %[1]s [options] -e PATTERN ... [FILE...]
   or: %[1]s [options] -e PATTERN ... -c -- [COMMAND...]

This utility is a simple filter, you can use to colorize output of any program.

Options:
  --bufsile=SIZE         buffer size which used to read line (default: %d);
  -c, --command          run COMMAND and filter output;
  -1, --stdout           filter stdout of COMMAND;
  -2, --stderr           filter stderr of COMMAND;
  -e, --regexp=PATTERN   use PATTERN for matching;
  -f, --file=FILE        obtain PATTERN from FILE;
  -V, --version          print program version and exit;
  -h, --help             show this text and exit.

Report bugs to author.

`,
		prog, bufferSize)
	os.Exit(0)
	return nil
}

func showVersion(*getopt.Option, getopt.NameType, string) error {
	fmt.Fprintf(os.Stdout, `%s version %s
Written by Alexey Gladkov.

Copyright (C) 2017  Alexey Gladkov <gladkov.alexey@gmail.com>
This is free software; see the source for copying conditions.  There is NO
warranty; not even for MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.
`,
		prog, version)
	os.Exit(0)
	return nil
}

func errorf(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s: ERROR: ", prog)
	fmt.Fprintf(os.Stderr, format, v...)
	fmt.Fprintf(os.Stderr, "\n")
}

func fatal(format string, v ...interface{}) {
	errorf(format, v...)
	os.Exit(1)
}

func parsePattern(filename string, num int, line string) (Pattern, error) {
	line = strings.TrimSpace(line)

	if len(line) == 0 {
		return Pattern{}, fmt.Errorf("%s:%d: bad format: empty rule", filename, num)
	}
	if line[0] != '/' {
		return Pattern{}, fmt.Errorf("%s:%d: bad format: unexpected begin of regular expression", filename, num)
	}
	last := strings.LastIndexByte(line, '/')
	if last == -1 {
		return Pattern{}, fmt.Errorf("%s:%d: bad format: can not find end of regular expression", filename, num)
	}
	if last <= 1 {
		return Pattern{}, fmt.Errorf("%s:%d: bad format: empty regular expression", filename, num)
	}

	re, err := regexp.Compile(line[1:last])
	if err != nil {
		return Pattern{}, fmt.Errorf("%s:%d: %v", filename, num, err)
	}

	pattern := Pattern{
		RE: re,
	}

	names := map[string]Colorize{}

	for i, s := range strings.Split(line[last+1:], ",") {
		if len(s) == 0 {
			continue
		}
		pair := strings.Split(s, ":")
		if len(pair) != 2 {
			return Pattern{}, fmt.Errorf("%s:%d: bad format: can not parse group %d", filename, num, i)
		}
		names[strings.TrimSpace(pair[0])] = ParseColorize(pair[1])
	}

	for i, name := range pattern.RE.SubexpNames() {
		if len(name) == 0 {
			continue
		}

		data, ok := names[name]
		if !ok {
			continue
		}

		pattern.Groups = append(pattern.Groups, Group{
			Name:     name,
			Number:   i,
			Colorize: data,
		})
	}

	return pattern, nil
}

func readPatternsFromFile(filename string, rd io.Reader) ([]Pattern, error) {
	var (
		line      string
		readerErr error
		patterns  []Pattern
	)
	lineNum := 0
	reader := bufio.NewReader(rd)

	for readerErr == nil {
		lineNum++
		line, readerErr = reader.ReadString('\n')

		if readerErr != nil && readerErr != io.EOF {
			return patterns, readerErr
		}

		line = strings.TrimSpace(line)

		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}

		pattern, err := parsePattern(filename, lineNum, line)
		if err != nil {
			return patterns, err
		}

		patterns = append(patterns, pattern)
	}

	return patterns, nil
}

func processFile(patterns []Pattern, rd io.Reader, wr io.Writer) error {
	var (
		line      []byte
		readerErr error
	)
	reader := bufio.NewReaderSize(rd, bufferSize)

	lineColorFG := make([]int, len(patterns))
	lineColorBG := make([]int, len(patterns))
	lineProperties := make(map[string]int, len(AnsiProperties))

	for readerErr == nil {
		line, readerErr = reader.ReadSlice('\n')

		if readerErr == bufio.ErrBufferFull {
			readerErr = nil
		}

		if readerErr != nil && readerErr != io.EOF {
			return readerErr
		}

		var (
			lineMatches bool
			positions   LinePositions
		)

		for n, pattern := range patterns {
			res := pattern.RE.FindAllSubmatchIndex(line, -1)
			if res == nil {
				continue
			}
			lineMatches = true
			for i, group := range pattern.Groups {
				for _, match := range res {
					pos := group.Number * 2
					if match[pos] == match[pos+1] {
						continue
					}
					positions = append(positions,
						&LinePosition{
							Kind:     LinePositionStartKind,
							Order:    n,
							Offset:   match[pos],
							Colorize: pattern.Groups[i].Colorize,
						},
						&LinePosition{
							Kind:     LinePositionEndKind,
							Order:    n,
							Offset:   match[pos+1],
							Colorize: pattern.Groups[i].Colorize,
						})
				}
			}
		}

		if len(positions) > 0 {
			sort.Sort(positions)

			lineOffset := 0
			prevEscape := ""

			for _, pos := range positions {
				if lineOffset < pos.Offset {
					wr.Write(line[lineOffset:pos.Offset])
					lineOffset = pos.Offset
				}
				if lineOffset == pos.Offset {
					switch pos.Kind {
					case LinePositionStartKind:
						for k := range AnsiProperties {
							if _, ok := pos.Colorize[k]; ok {
								lineProperties[k]++
							}
						}
						lineColorFG[pos.Order] = pos.Colorize[ForegroundColor]
						lineColorBG[pos.Order] = pos.Colorize[BackgroundColor]
					case LinePositionEndKind:
						for k := range AnsiProperties {
							if _, ok := pos.Colorize[k]; ok {
								lineProperties[k]--
							}
						}
						lineColorFG[pos.Order] = 0
						lineColorBG[pos.Order] = 0
					}

					var foundFG, foundBG int

					for n := len(patterns) - 1; n >= 0 && (foundFG == 0 || foundBG == 0); n-- {
						if foundFG == 0 && lineColorFG[n] > 0 {
							foundFG = lineColorFG[n]
						}
						if foundBG == 0 && lineColorBG[n] > 0 {
							foundBG = lineColorBG[n]
						}
					}
					if foundFG == 0 {
						foundFG = ResetForeground
					}
					if foundBG == 0 {
						foundBG = ResetBackground
					}
					props := ""
					for k, v := range lineProperties {
						props += fmt.Sprintf("%d;", Property(k, v > 0))
					}

					escape := fmt.Sprintf("%s%s%d;%dm", AnsiStart, props, foundFG, foundBG)

					if prevEscape != escape {
						wr.Write([]byte(escape))
						prevEscape = escape
					}
				}
			}
			wr.Write(line[lineOffset:])
		} else if lineMatches {
			wr.Write(line)
		}
	}

	return nil
}

func syncStdStreams() {
	os.Stdout.Sync()
	os.Stderr.Sync()
}

type CommandFilter struct {
	Patterns []Pattern
	Stdout   bool
	Stderr   bool
}

func processCommand(filter *CommandFilter, name string, args ...string) {
	var wg sync.WaitGroup

	cmd := exec.Command(name, args...)

	if filter.Stdout {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			fatal("%v\n", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := processFile(filter.Patterns, stdout, os.Stdout)
			syncStdStreams()
			if err != nil {
				fatal("%v\n", err)
			}
		}()
	} else {
		cmd.Stdout = os.Stdout
	}

	if filter.Stderr {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			fatal("%v\n", err)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := processFile(filter.Patterns, stderr, os.Stderr)
			syncStdStreams()
			if err != nil {
				fatal("%v\n", err)
			}
		}()
	} else {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		fatal("%v\n", err)
	}

	wg.Wait()

	retCode := 0
	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				retCode = status.ExitStatus()
			}
		} else {
			syncStdStreams()
			errorf("%v\n", err)
			retCode = 1
		}
	}
	os.Exit(retCode)
}

func main() {
	var (
		cmdStdout    bool
		cmdStderr    bool
		cmdFilter    bool
		regexps      []string
		patternsFile string
	)
	opts := &getopt.Getopt{
		AllowAbbrev: true,
		Options: []getopt.Option{
			{'h', "help", getopt.NoArgument,
				showUsage,
			},
			{'V', "version", getopt.NoArgument,
				showVersion,
			},
			{getopt.NoShortName, "bufsize", getopt.RequiredArgument,
				func(o *getopt.Option, t getopt.NameType, v string) (err error) {
					bufferSize, err = strconv.Atoi(v)
					return
				},
			},
			{'f', "file", getopt.RequiredArgument,
				func(o *getopt.Option, t getopt.NameType, v string) (err error) {
					info, err := os.Stat(v)
					if err != nil {
						return
					}
					if info.IsDir() {
						return fmt.Errorf("filename required")
					}
					patternsFile = v
					return
				},
			},
			{'e', "regexp", getopt.RequiredArgument,
				func(o *getopt.Option, t getopt.NameType, v string) error {
					regexps = append(regexps, v)
					return nil
				},
			},
			{'c', "command", getopt.NoArgument,
				func(o *getopt.Option, t getopt.NameType, v string) error {
					cmdFilter = true
					return nil
				}},
			{'1', "stdout", getopt.NoArgument,
				func(o *getopt.Option, t getopt.NameType, v string) error {
					cmdStdout = true
					return nil
				},
			},
			{'2', "stderr", getopt.NoArgument,
				func(o *getopt.Option, t getopt.NameType, v string) error {
					cmdStderr = true
					return nil
				},
			},
		},
	}

	prog = filepath.Base(os.Args[0])
	if err := opts.Parse(os.Args); err != nil {
		fatal("%v", err)
	}
	args := opts.Args()

	patterns := []Pattern{}

	if len(patternsFile) > 0 {
		fd, err := os.Open(patternsFile)
		if err != nil {
			fatal("%v", err)
		}
		defer fd.Close()

		patterns, err = readPatternsFromFile(patternsFile, fd)
		if err != nil {
			fatal("%v", err)
		}
		fd.Close()
	}

	for i, s := range regexps {
		pattern, err := parsePattern("Arg", i+1, s)
		if err != nil {
			fatal("%v", err)
		}
		patterns = append(patterns, pattern)
	}

	if len(patterns) == 0 {
		fatal("patterns required")
	}

	if cmdFilter {
		filter := &CommandFilter{
			Patterns: patterns,
			Stdout:   cmdStdout,
			Stderr:   cmdStderr,
		}
		if len(args) > 1 {
			processCommand(filter, args[0], args[1:]...)
		} else {
			processCommand(filter, args[0])
		}
		return
	} else if cmdStdout {
		fatal("option --stdout implies the --command")
	} else if cmdStderr {
		fatal("option --stderr implies the --command")
	}

	if len(args) == 0 {
		if err := processFile(patterns, os.Stdin, os.Stdout); err != nil {
			fatal("%v", err)
		}
	}

	for _, filename := range args {
		fd, err := os.Open(filename)
		if err != nil {
			fatal("%s: %v", filename, err)
		}
		defer fd.Close()

		if err := processFile(patterns, fd, os.Stdout); err != nil {
			fatal("%v", err)
		}
		fd.Close()
	}
}
