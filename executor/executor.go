// Copyright 2020 Marius van der Wijden
// This file is part of the fuzzy-vm library.
//
// The fuzzy-vm library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The fuzzy-vm library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the fuzzy-vm library. If not, see <http://www.gnu.org/licenses/>.

// Package executor executes state tests and compares results.
package executor

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/metrics"
	"github.com/holiman/goevmlab/evms"
	"github.com/korovkin/limiter"
	"github.com/pkg/errors"
)

var PrintTrace = true

type Executor struct {
	Vms        []evms.Evm
	PrintTrace bool
}

func NewExecutor(vms []evms.Evm, printTrace bool) *Executor {
	return &Executor{
		Vms:        vms,
		PrintTrace: printTrace,
	}
}

// Execute runs all tests in `dirName` and saves crashers in `outDir`
func (e *Executor) Execute(dirName, outDir string, threadlimit int) error {
	infos, err := ioutil.ReadDir(dirName)
	if err != nil {
		return err
	}
	errChan := make(chan error)
	limit := limiter.NewConcurrencyLimiter(threadlimit)
	meter := metrics.GetOrRegisterMeterForced("ticks", nil)

	for i, info := range infos {
		// All generated tests end in .json
		if strings.HasSuffix(info.Name(), ".json") {
			fmt.Printf("Executing test: %v of %v, %f per second \n", i/2, len(infos)/2, meter.Rate1())
			meter.Mark(1)
			name := info.Name()
			job := func() {
				if err := e.ExecuteFullTest(dirName, outDir, name, true); err != nil {
					err := errors.Wrap(err, fmt.Sprintf("in file: %v", name))
					fmt.Println(err)
					//errChan <- err
				}
			}
			limit.Execute(job)
		}
	}
	limit.Wait()
	for {
		select {
		case err := <-errChan:
			fmt.Println(err)
		default:
			// All tests sucessfully executed
			return nil
		}
	}
}

// ExecuteFullTest executes a single test.
func (e *Executor) ExecuteFullTest(dirName, outDir, filename string, doPurge bool) error {
	var (
		testFile  = fmt.Sprintf("%v/%v", dirName, filename)
		testName  = strings.TrimRight(filename, ".json")
		traceFile = fmt.Sprintf("%v/%v-trace.jsonl", dirName, testName)
		outputs   [][]byte
		err       error
	)
	outputs, err = e.ExecuteTest(testFile)
	if err != nil {
		return err
	}
	return e.verifyAndPurge(traceFile, testName, outDir, testFile, outputs, doPurge)
}

func (e *Executor) verifyAndPurge(traceFile, testName, outDir, testFile string, outputs [][]byte, doPurge bool) error {
	if !e.Verify(traceFile, outputs) {
		fmt.Printf("Test %v failed, dumping\n", testName)
		if err := dump(testName, outDir, e.Vms, outputs); err != nil {
			return err
		}
	} else {
		if doPurge {
			if err := purge(testFile, traceFile); err != nil {
				// Ignore purging errors
				fmt.Printf("Purging failed: %v\n", err)
			}
		} else if PrintTrace {
			e.printOutputs(outputs)
		}
	}
	return nil
}

// ExecuteTest executes a state test.
func (e *Executor) ExecuteTest(testName string) ([][]byte, error) {
	var buf [][]byte
	var buffer bytes.Buffer
	for _, vm := range e.Vms {
		buffer.Reset()
		if _, err := vm.RunStateTest(testName, &buffer, false); err != nil {
			return nil, err
		}
		buf = append(buf, buffer.Bytes())
	}
	return buf, nil
}

// Verify checks if the traces match the default trace.
func (e *Executor) Verify(traceName string, outputs [][]byte) bool {
	var ioReaders []io.Reader
	for _, out := range outputs {
		ioReaders = append(ioReaders, bytes.NewReader(out))
	}
	// Add the standard trace to the test (currently deactivated)
	/*
		ref, err := ioutil.ReadFile(traceName)
		if err != nil {
			return false
		}
		ioReaders = append(ioReaders, bytes.NewBuffer(ref))
	*/
	return evms.CompareFiles(e.Vms, ioReaders)
}

// dump writes outputs to a file in case of a verification problem
func dump(filename, outdir string, vms []evms.Evm, outputs [][]byte) error {
	for i, out := range outputs {
		filename := fmt.Sprintf("%v/%v-%v-trace.jsonl", outdir, filename, vms[i].Name())
		f, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0755)
		if err != nil {
			return err
		}
		if _, err := f.Write(out); err != nil {
			return err
		}
	}
	return nil
}

// purge deletes a test file and its corresponding trace
func purge(filename, tracename string) error {
	os.Remove(tracename)
	return os.Remove(filename)
}

// printOutputs prints out the produced traces
func (e *Executor) printOutputs(outputs [][]byte) {
	fmt.Println("TRACES:")
	fmt.Println("--------------")
	for i, out := range outputs {
		fmt.Printf("%v: \n", e.Vms[i].Name())
		fmt.Print(string(out))
		fmt.Println("--------------")
	}
}
