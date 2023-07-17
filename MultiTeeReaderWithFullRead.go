package httpCollapseProxy

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// Errors
var (
	ErrReadingCommenced = errors.New("reading has commenced and cannot add")
)

// MultiTeeReaderWithFullRead
// Copies Read() into multiple Writers
// Also if the main Read doesn't complete till EOF
// completes the Read till EOF and Writes to Writers before Closing
type MultiTeeReaderWithFullRead struct {
	byteWritten atomic.Bool
	reader      io.ReadCloser
	writers     []io.WriteCloser
	writeWg     sync.WaitGroup
	copyBuff    []byte
	COPYBUFFLEN int
}

// Creates MultiTeeReaderWithFullRead with Multiple Writers
func NewMultiTeeReaderWithFullRead(r io.ReadCloser, w []io.WriteCloser) *MultiTeeReaderWithFullRead {
	return &MultiTeeReaderWithFullRead{
		byteWritten: atomic.Bool{},
		reader:      r,
		writers:     w,
		writeWg:     sync.WaitGroup{},
		copyBuff:    []byte{},
		COPYBUFFLEN: 4096,
	}
}

// Creates MultiTeeReaderWithFullRead with Multiple Writers
func (d *MultiTeeReaderWithFullRead) AddWriter(w io.WriteCloser) error {
	if d.byteWritten.Load() {
		return ErrReadingCommenced
	}
	// add Writer to the list
	d.writers = append(d.writers, w)
	return nil
}

// Read issued by main Consumer
func (d *MultiTeeReaderWithFullRead) Read(p []byte) (n int, err error) {
	// wait for previous writes to complete
	d.writeWg.Wait()
	// Read new content
	n, err = d.reader.Read(p)
	if n <= 0 {
		// nothing to write
		return
	}
	if !d.byteWritten.Load() {
		// if no bytes written still
		d.byteWritten.Store(true)
	}
	// we need to copy content... lockup further reads
	d.writeWg.Add(len(d.writers))
	// copy buffer
	if &p != &d.copyBuff {
		// incoming buffer is not copy buffer
		// check the len of buffer
		if n > cap(d.copyBuff) {
			//adjust if required
			d.copyBuff = make([]byte, 0, n)
		} else {
			// retain capacity
			d.copyBuff = d.copyBuff[0:0]
		}
		d.copyBuff = append(d.copyBuff, p[0:n]...)
	}
	// Write to each Writer
	for _, w := range d.writers {
		go func(w io.WriteCloser) {
			defer d.writeWg.Done()
			nW, errW := w.Write(d.copyBuff)
			if errW != nil {
				switch errW {
				case io.ErrClosedPipe: //ignore
					return
				default:
					// Write failed : LOG/METRIC?
				}
			}
			if n != nW {
				// Write failed : LOG/METRIC?
			}
		}(w)
	}
	return
}

// reads till EOF of main Reader
// writes to the writers
func (d *MultiTeeReaderWithFullRead) readTillEof() error {
	if d.copyBuff == nil || len(d.copyBuff) < d.COPYBUFFLEN {
		d.copyBuff = make([]byte, d.COPYBUFFLEN)
	}
	for {
		_, err := d.Read(d.copyBuff)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Close the Reader
// Read till EOF and write to the writers
// Close Reader
// Close Writers
func (d *MultiTeeReaderWithFullRead) Close() error {
	d.readTillEof()
	d.writeWg.Wait()
	err := d.reader.Close()
	for _, w := range d.writers {
		d.writeWg.Add(1)
		go func(w io.WriteCloser) {
			defer d.writeWg.Done()
			errC := w.Close()
			if errC != nil {
				// Close failed
			}
		}(w)
	}
	return err
}
