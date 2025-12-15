package tunnel

import (
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestYamuxMultiStream tests multiple concurrent streams over a single yamux session
func TestYamuxMultiStream(t *testing.T) {
	// Create a pair of connected yamux sessions
	client, server := createYamuxPair(t)
	defer client.Close()
	defer server.Close()

	streamCount := 10
	messageSize := 1024
	var wg sync.WaitGroup

	// Server: accept and echo data
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < streamCount; i++ {
			stream, err := server.AcceptStream()
			if err != nil {
				return
			}

			go func(s *yamux.Stream) {
				defer s.Close()
				buf := make([]byte, messageSize)
				n, err := s.Read(buf)
				if err != nil {
					return
				}
				s.Write(buf[:n])
			}(stream)
		}
	}()

	// Client: open streams and send data
	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			stream, err := client.OpenStream()
			require.NoError(t, err)
			defer stream.Close()

			// Send data
			testData := []byte(fmt.Sprintf("test message %d", id))
			padded := make([]byte, messageSize)
			copy(padded, testData)

			n, err := stream.Write(padded)
			require.NoError(t, err)
			assert.Equal(t, messageSize, n)

			// Read echo
			buf := make([]byte, messageSize)
			n, err = stream.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, messageSize, n)
			assert.Equal(t, padded, buf)
		}(i)
	}

	wg.Wait()
	t.Logf("Successfully processed %d concurrent streams", streamCount)
}

// TestYamuxStressConcurrent tests many concurrent streams with varying data sizes
func TestYamuxStressConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	client, server := createYamuxPair(t)
	defer client.Close()
	defer server.Close()

	streamCount := 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0
	errorCount := 0

	// Server: accept and echo
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < streamCount; i++ {
			stream, err := server.AcceptStream()
			if err != nil {
				return
			}

			go func(s *yamux.Stream) {
				defer s.Close()
				io.Copy(s, s) // Echo
			}(stream)
		}
	}()

	// Client: open streams concurrently
	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			stream, err := client.OpenStream()
			if err != nil {
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}
			defer stream.Close()

			// Send varying size data
			size := 100 + (id * 10)
			testData := make([]byte, size)
			for j := range testData {
				testData[j] = byte(j % 256)
			}

			_, err = stream.Write(testData)
			if err != nil {
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			// Read back
			buf := make([]byte, size)
			n, err := io.ReadFull(stream, buf)
			if err != nil || n != size {
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			if string(buf) == string(testData) {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Success: %d, Errors: %d", successCount, errorCount)
	assert.Equal(t, streamCount, successCount+errorCount)
	assert.Less(t, errorCount, 5, "too many errors")
}


// Helper: create a pair of connected yamux sessions
func createYamuxPair(t *testing.T) (*yamux.Session, *yamux.Session) {
	// Create in-memory pipe
	clientConn, serverConn := createConnPair()

	// Create yamux sessions
	clientSession, err := yamux.Client(clientConn, nil)
	require.NoError(t, err)

	serverSession, err := yamux.Server(serverConn, nil)
	require.NoError(t, err)

	return clientSession, serverSession
}

// Helper: create a pair of connected net.Conn (in-memory pipe)
func createConnPair() (*pipeConn, *pipeConn) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	conn1 := &pipeConn{reader: r1, writer: w2}
	conn2 := &pipeConn{reader: r2, writer: w1}

	return conn1, conn2
}

// pipeConn implements net.Conn using io.Pipe
type pipeConn struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (c *pipeConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func (c *pipeConn) Write(b []byte) (int, error) {
	return c.writer.Write(b)
}

func (c *pipeConn) Close() error {
	c.reader.Close()
	c.writer.Close()
	return nil
}

func (c *pipeConn) LocalAddr() any  { return nil }
func (c *pipeConn) RemoteAddr() any { return nil }
func (c *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return nil }
