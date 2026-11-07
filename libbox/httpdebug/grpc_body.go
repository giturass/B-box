package httpdebug

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type grpcBodyFormat uint8

const (
	grpcNone grpcBodyFormat = iota
	grpcNative
	grpcWeb
	grpcWebText
)

func grpcFormat(header http.Header) grpcBodyFormat {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(header.Get("Content-Type"), ";", 2)[0]))
	for _, entry := range []struct {
		name string
		kind grpcBodyFormat
	}{{"application/grpc", grpcNative}, {"application/grpc-web", grpcWeb}, {"application/grpc-web-text", grpcWebText}} {
		if mediaType == entry.name || strings.HasPrefix(mediaType, entry.name+"+") {
			return entry.kind
		}
	}
	return grpcNone
}

// HTTP headers do not identify an RPC's cardinality. Only an explicit unary
// script may buffer gRPC; broad HTTP body rules must never stall a duplex RPC.
type unaryGRPCBody struct {
	format   grpcBodyFormat
	limit    int64
	input    http.Header
	trailers []byte
}

func unaryGRPCScript(header http.Header, script *compiledScript) *unaryGRPCBody {
	format := grpcFormat(header)
	if format == grpcNone || script == nil || script.options.GRPCMode != "unary" {
		return nil
	}
	return &unaryGRPCBody{format: format, limit: script.options.MaxBodyBytes, input: header.Clone()}
}

// The script receives one complete length-prefixed message, including its
// compression flag. HTTP trailers and grpc-web trailer frames remain owned by
// the transport. In web-text mode base64 is decoded/re-encoded outside the VM.
func (g *unaryGRPCBody) prepare(data []byte, response bool, note func(string)) ([]byte, bool, error) {
	wire := data
	var err error
	if g.format == grpcWebText {
		wire, err = decodeGRPCWebText(data, g.limit)
	}
	end := 0
	if err == nil {
		end, err = inspectUnaryGRPC(wire, g.input, g.format != grpcNative && response, g.limit)
	}
	if err != nil {
		if errors.Is(err, errRewriteSize) && !response {
			return nil, false, err
		}
		note("gRPC unary body skipped: " + err.Error() + "; original stream preserved")
		return nil, false, nil
	}
	if end == 0 {
		note("gRPC unary body skipped: no data message; original status and trailers preserved")
		return nil, false, nil
	}
	g.trailers = wire[end:]
	return wire[:end], true, nil
}

func inspectUnaryGRPC(wire []byte, header http.Header, webResponse bool, limit int64) (int, error) {
	messageEnd := 0
	for offset := 0; offset < len(wire); {
		if len(wire)-offset < 5 {
			return 0, errors.New("incomplete gRPC frame header")
		}
		flag := wire[offset]
		trailer := flag&0x80 != 0
		if flag&0x7e != 0 || trailer && !webResponse {
			return 0, errors.New("invalid gRPC frame flag")
		}
		size := int64(binary.BigEndian.Uint32(wire[offset+1 : offset+5]))
		if size > int64(len(wire)-offset-5) {
			return 0, errors.New("gRPC message length does not match body")
		}
		end := offset + 5 + int(size)
		if !trailer && messageEnd != 0 {
			return 0, errors.New("multiple gRPC data messages; unary mode does not process streaming RPCs")
		}
		if trailer && end != len(wire) {
			return 0, errors.New("grpc-web trailers must be the last frame")
		}
		if flag&1 != 0 {
			encoding := strings.ToLower(strings.TrimSpace(header.Get("Grpc-Encoding")))
			if len(header.Values("Grpc-Encoding")) != 1 || encoding != "gzip" && encoding != "deflate" {
				return 0, fmt.Errorf("unsupported gRPC message compression %q", encoding)
			}
			// Bound the uncompressed message as well as the serialized HTTP body.
			// Keep the original frame for Surge-compatible binary scripts.
			if limit < 5 {
				return 0, errRewriteSize
			}
			if _, err := decodeContent(wire[offset+5:end], []string{encoding}, limit-5); err != nil {
				return 0, fmt.Errorf("gRPC message decompression: %w", err)
			}
		}
		if !trailer {
			messageEnd = end
		}
		offset = end
	}
	return messageEnd, nil
}

func (g *unaryGRPCBody) result(result scriptResult, original http.Header) (scriptResult, error) {
	if !result.bodySet || result.abort || result.response != nil {
		return result, nil
	}
	header := original
	if result.headers != nil {
		header = result.headers
	}
	if grpcFormat(header) != g.format {
		return scriptResult{}, errors.New("gRPC body replacement must preserve its Content-Type")
	}
	end, err := inspectUnaryGRPC(result.body, header, false, g.limit)
	if err != nil {
		return scriptResult{}, err
	}
	if end == 0 {
		return scriptResult{}, errors.New("gRPC body replacement requires one complete length-prefixed message")
	}
	length := int64(len(result.body)) + int64(len(g.trailers))
	if g.format == grpcWebText {
		length = (length + 2) / 3 * 4
	}
	if length > g.limit {
		return scriptResult{}, errRewriteSize
	}
	if len(g.trailers) > 0 {
		result.body = append(bytesClone(result.body), g.trailers...)
	}
	if g.format == grpcWebText {
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(result.body)))
		base64.StdEncoding.Encode(encoded, result.body)
		result.body = encoded
	}
	return result, nil
}

// grpc-web-text may contain independently padded base64 chunks, even inside
// a message. A single DecodeString would reject valid in-stream padding.
func decodeGRPCWebText(wire []byte, limit int64) ([]byte, error) {
	clean := make([]byte, 0, len(wire))
	for _, value := range wire {
		if value != '\r' && value != '\n' {
			clean = append(clean, value)
		}
	}
	decoded := make([]byte, 0, min(int64(base64.StdEncoding.DecodedLen(len(clean))), limit))
	for len(clean) > 0 {
		end := len(clean)
		if padding := bytes.IndexByte(clean, '='); padding >= 0 {
			end = (padding/4 + 1) * 4
		}
		if end > len(clean) || end%4 != 0 {
			return nil, errors.New("invalid grpc-web-text base64 padding")
		}
		block := make([]byte, base64.StdEncoding.DecodedLen(end))
		n, err := base64.StdEncoding.Decode(block, clean[:end])
		if err != nil {
			return nil, fmt.Errorf("invalid grpc-web-text body: %w", err)
		}
		if int64(len(decoded)+n) > limit {
			return nil, errRewriteSize
		}
		decoded = append(decoded, block[:n]...)
		clean = clean[end:]
	}
	return decoded, nil
}

func replaceGRPCRequestBody(request *http.Request, data []byte) {
	trailers := request.Trailer
	replaceRequestBody(request, data)
	request.Trailer = trailers
	if len(trailers) > 0 {
		request.ContentLength = -1
		request.Header.Del("Content-Length")
	}
}

func replaceGRPCResponseBody(response *http.Response, data []byte) {
	trailers := response.Trailer
	replaceResponseBody(response, data)
	response.Trailer = trailers
	if len(trailers) > 0 {
		response.ContentLength = -1
		response.Header.Del("Content-Length")
	}
}
