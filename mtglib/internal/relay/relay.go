package relay

import (
	"context"
	"errors"
	"io"

	"github.com/9seconds/mtg/v2/essentials"
)

type TrafficLogger interface {
	Log(tx, rx uint64) (ok bool)
}

func Relay(ctx context.Context, log Logger, telegramConn, clientConn essentials.Conn, traffic func(tx, rx uint64) (ok bool)) {
	defer telegramConn.Close()
	defer clientConn.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		telegramConn.Close()
		clientConn.Close()
	}()

	closeChan := make(chan struct{})

	go func() {
		defer close(closeChan)

		pump(log, telegramConn, clientConn, traffic, "client -> telegram")
	}()

	pump(log, clientConn, telegramConn, traffic, "telegram -> client")

	<-closeChan
}

func pump(log Logger, src, dst essentials.Conn, traffic func(tx, rx uint64) (ok bool), direction string) {
	defer src.CloseRead()  //nolint: errcheck
	defer dst.CloseWrite() //nolint: errcheck

	copyBuffer := acquireCopyBuffer()
	defer releaseCopyBuffer(copyBuffer)

	var (
		n   int64
		err error
	)
	switch direction {
	case "client -> telegram":
		if traffic == nil {
			n, err = io.CopyBuffer(dst, src, *copyBuffer)
		} else {
			n, err = CopyBufferTraffic(dst, src, *copyBuffer, func(u uint64) (ok bool) {
				return traffic(0, u)
			})
		}
	case "telegram -> client":
		if traffic == nil {
			n, err = io.CopyBuffer(dst, src, *copyBuffer)
		} else {
			n, err = CopyBufferTraffic(dst, src, *copyBuffer, func(u uint64) (ok bool) {
				return traffic(u, 0)
			})
		}
	default:
		err = errors.New("unknown direction")
	}

	switch {
	case err == nil:
		log.Printf("%s has been finished", direction)
	case errors.Is(err, io.EOF):
		log.Printf("%s has been finished because of EOF. Written %d bytes", direction, n)
	default:
		log.Printf("%s has been finished (written %d bytes): %v", direction, n, err)
	}
}

func CopyBufferTraffic(dst io.Writer, src io.Reader, buf []byte, traffic func(uint64) (ok bool)) (written int64, err error) {
	if buf == nil {
		size := 32 * 1024
		if l, ok := src.(*io.LimitedReader); ok && int64(size) > l.N {
			if l.N < 1 {
				size = 1
			} else {
				size = int(l.N)
			}
		}
		buf = make([]byte, size)
	}
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			if !traffic(uint64(nr)) {
				return written, io.ErrClosedPipe
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}
