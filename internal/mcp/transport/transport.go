package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
)

// jsonrpcVersion is the MCP protocol version.
const jsonrpcVersion = "2.0"

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *json.RawMessage `json:"id,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
	ID      *json.RawMessage `json:"id,omitempty"`
}

// ResponseError is a JSON-RPC 2.0 error.
type ResponseError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error codes from JSON-RPC 2.0 spec.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Handler is a function that handles a JSON-RPC method call.
type Handler func(ctx context.Context, params json.RawMessage) (interface{}, error)

// Server is a JSON-RPC 2.0 server that reads requests from stdin and writes
// responses to stdout.
type Server struct {
	handlers map[string]Handler
	mu       sync.Mutex
	in       io.Reader
	out      io.Writer
	logger   *log.Logger
}

// NewServer creates a new JSON-RPC server reading from in and writing to out.
func NewServer(in io.Reader, out io.Writer) *Server {
	return &Server{
		handlers: make(map[string]Handler),
		in:       in,
		out:      out,
		logger:   log.New(io.Discard, "", 0),
	}
}

// WithLogger sets the logger for the server.
func (s *Server) WithLogger(logger *log.Logger) *Server {
	s.logger = logger
	return s
}

// Handle registers a handler for a method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Start reads requests from in, dispatches to handlers, and writes responses
// to out. It blocks until in is closed.
func (s *Server) Start(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024*4)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(ctx, nil, &ResponseError{
				Code:    CodeParseError,
				Message: "Parse error",
			}, nil)
			continue
		}

		if req.Method == "" {
			s.writeResponse(ctx, req.ID, &ResponseError{
				Code:    CodeInvalidRequest,
				Message: "Invalid Request",
			}, nil)
			continue
		}

		s.mu.Lock()
		handler, ok := s.handlers[req.Method]
		s.mu.Unlock()

		if !ok {
			s.writeResponse(ctx, req.ID, &ResponseError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			}, nil)
			continue
		}

		result, err := handler(ctx, req.Params)
		if err != nil {
			s.writeResponse(ctx, req.ID, &ResponseError{
				Code:    CodeInternalError,
				Message: err.Error(),
			}, nil)
		} else {
			s.writeResponse(ctx, req.ID, &ResponseError{}, result)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// writeResponse sends a JSON-RPC response to out.
func (s *Server) writeResponse(ctx context.Context, id *json.RawMessage, err *ResponseError, result interface{}) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	resp := Response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
	}

	if err != nil && (err.Code != 0 || err.Message != "") {
		resp.Error = err
	} else {
		resp.Result = result
	}

	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		s.logger.Printf("failed to marshal response: %v", marshalErr)
		return
	}

	data = append(data, '\n')
	if _, writeErr := s.out.Write(data); writeErr != nil {
		s.logger.Printf("failed to write response: %v", writeErr)
	}
}

// Run starts the server on stdin/stdout and blocks until done.
func Run(ctx context.Context, s *Server) error {
	return s.Start(ctx)
}
