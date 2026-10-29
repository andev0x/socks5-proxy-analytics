package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
)

const (
	socksVersion = byte(0x05)
	authVersion  = byte(0x01)

	methodNoAuth       = byte(0x00)
	methodUserPassword = byte(0x02)
	methodNoAcceptable = byte(0xFF)

	commandConnect = byte(0x01)

	addressIPv4   = byte(0x01)
	addressDomain = byte(0x03)
	addressIPv6   = byte(0x04)

	replySucceeded           = byte(0x00)
	replyGeneralFailure      = byte(0x01)
	replyNotAllowed          = byte(0x02)
	replyNetworkUnreachable  = byte(0x03)
	replyHostUnreachable     = byte(0x04)
	replyConnectionRefused   = byte(0x05)
	replyCommandNotSupported = byte(0x07)
	replyAddressNotSupported = byte(0x08)
)

type handshakeError struct {
	reason string
}

func (e *handshakeError) Error() string {
	return e.reason
}

type request struct {
	workloadID    string
	requestedHost string
	dialHost      string
	port          int
}

// handshake performs SOCKS5 method negotiation, optional RFC 1929 authentication and
// request parsing. It returns a buffered reader that may contain pipelined payload bytes.
func (s *Server) handshake(conn net.Conn) (*request, *bufio.Reader, error) {
	reader := bufio.NewReader(conn)

	workloadID, err := s.negotiateAuth(conn, reader)
	if err != nil {
		return nil, nil, err
	}

	req, err := readRequest(reader)
	if err != nil {
		var handshakeErr *handshakeError
		if errors.As(err, &handshakeErr) {
			code := replyCodeForReason(handshakeErr.reason)
			if code != 0 {
				_ = writeReply(conn, code, nil)
			}
		}

		return nil, nil, err
	}

	req.workloadID = workloadID

	return req, reader, nil
}

func (s *Server) negotiateAuth(conn net.Conn, reader *bufio.Reader) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedGreeting}
	}

	if header[0] != socksVersion {
		return "", &handshakeError{reason: metrics.HandshakeMalformedGreeting}
	}

	methodCount := int(header[1])
	if methodCount == 0 {
		_, _ = conn.Write([]byte{socksVersion, methodNoAcceptable})

		return "", &handshakeError{reason: metrics.HandshakeMalformedGreeting}
	}

	methods := make([]byte, methodCount)
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedGreeting}
	}

	requiredMethod := methodNoAuth
	if s.auth != nil {
		requiredMethod = methodUserPassword
	}

	if !containsMethod(methods, requiredMethod) {
		_, _ = conn.Write([]byte{socksVersion, methodNoAcceptable})

		return "", &handshakeError{reason: metrics.HandshakeNoAcceptableAuth}
	}

	if _, err := conn.Write([]byte{socksVersion, requiredMethod}); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedGreeting}
	}

	if s.auth == nil {
		return "", nil
	}

	return s.authenticateUserPass(conn, reader)
}

func (s *Server) authenticateUserPass(conn net.Conn, reader *bufio.Reader) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != authVersion {
		_, _ = conn.Write([]byte{authVersion, 0x01})

		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	username := make([]byte, int(header[1]))
	passwordLength := make([]byte, 1)

	if len(username) == 0 {
		_, _ = conn.Write([]byte{authVersion, 0x01})
		s.m.AuthFailures.Inc()

		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	if _, err := io.ReadFull(reader, username); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	if _, err := io.ReadFull(reader, passwordLength); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	password := make([]byte, int(passwordLength[0]))
	if _, err := io.ReadFull(reader, password); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	workloadID, ok := s.auth.AuthenticateWithIdentity(string(username), string(password))
	if !ok {
		_, _ = conn.Write([]byte{authVersion, 0x01})
		s.m.AuthFailures.Inc()

		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	if _, err := conn.Write([]byte{authVersion, 0x00}); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeAuthFailed}
	}

	return workloadID, nil
}

func containsMethod(methods []byte, method byte) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}

	return false
}

func readRequest(reader *bufio.Reader) (*request, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	if header[0] != socksVersion || header[2] != 0x00 {
		return nil, &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	command := header[1]
	host, isDomain, err := readAddress(reader, header[3])
	if err != nil {
		return nil, err
	}

	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return nil, &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	port := int(portBytes[0])<<8 | int(portBytes[1])
	if port == 0 {
		return nil, &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	if command != commandConnect {
		return nil, &handshakeError{reason: metrics.HandshakeUnsupportedCmd}
	}

	req := &request{dialHost: host, port: port}
	if isDomain {
		req.requestedHost = host
	}

	return req, nil
}

func readAddress(reader *bufio.Reader, addressType byte) (host string, isDomain bool, err error) {
	switch addressType {
	case addressIPv4:
		host, err := readIPAddress(reader, net.IPv4len)

		return host, false, err
	case addressIPv6:
		host, err := readIPAddress(reader, net.IPv6len)

		return host, false, err
	case addressDomain:
		host, err := readDomainAddress(reader)

		return host, true, err
	default:
		return "", false, &handshakeError{reason: metrics.HandshakeUnsupportedAddr}
	}
}

func readIPAddress(reader *bufio.Reader, length int) (string, error) {
	raw := make([]byte, length)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	return net.IP(raw).String(), nil
}

func readDomainAddress(reader *bufio.Reader) (string, error) {
	length := make([]byte, 1)
	if _, err := io.ReadFull(reader, length); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	if length[0] == 0 {
		return "", &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	raw := make([]byte, int(length[0]))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	host := string(raw)
	if strings.ContainsRune(host, 0) {
		return "", &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	return host, nil
}

func replyCodeForReason(reason string) byte {
	switch reason {
	case metrics.HandshakeUnsupportedCmd:
		return replyCommandNotSupported
	case metrics.HandshakeUnsupportedAddr:
		return replyAddressNotSupported
	case metrics.HandshakeMalformedRequest:
		return replyGeneralFailure
	default:
		return 0
	}
}

func writeReply(conn net.Conn, code byte, bind net.Addr) error {
	ip := net.IPv4zero
	port := 0

	if tcpAddr, ok := bind.(*net.TCPAddr); ok {
		ip = tcpAddr.IP
		port = tcpAddr.Port
	}

	response := make([]byte, 0, 22)
	response = append(response, socksVersion, code, 0x00)

	if ip4 := ip.To4(); ip4 != nil {
		response = append(response, addressIPv4)
		response = append(response, ip4...)
	} else {
		response = append(response, addressIPv6)
		response = append(response, ip.To16()...)
	}

	response = append(response, byte(port>>8), byte(port))

	_, err := conn.Write(response)
	if err != nil {
		return fmt.Errorf("failed to write SOCKS5 reply: %w", err)
	}

	return nil
}

func requestAddress(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
