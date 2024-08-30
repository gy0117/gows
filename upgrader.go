package gosocket

import (
	"bufio"
	"github.com/gy/gosocket/internal"
	"net"
	"net/http"
)

type HookFunc interface {
}

type Upgrade struct {
	options      *ServerOptions // Upgrade与ServerOptions绑定
	eventHandler EventHandler
}

func NewUpgrade(eventHandler EventHandler, options *ServerOptions) *Upgrade {
	initServerOptions(options)
	return &Upgrade{
		eventHandler: eventHandler,
		options:      options,
	}
}

// Upgrade
// 升级HTTP连接成websocket
func (up *Upgrade) Upgrade(w http.ResponseWriter, r *http.Request) (*WsConn, error) {
	// 预处理请求，但是预处理不是框架应该做的事情，应该交由调用者处理！！！
	sm := up.options.NewSessionMap()
	if err := up.options.PreSessionHandle(r, sm); err != nil {
		return nil, err
	}

	return up.upgradeInner(w, r, sm)
}

// 劫持HTTP连接，升级成websocket
func (up *Upgrade) upgradeInner(w http.ResponseWriter, r *http.Request, sm SessionManager) (*WsConn, error) {
	// 1. 劫持
	netConn, _, err := up.hijack(w)
	if err != nil {
		return nil, err
	}
	// 维护缓冲区池子，不使用hijack返回的reader
	reader := up.options.readerBufPool.Get()
	reader.Reset(netConn)

	// 2. 升级成websocket
	// 2.1 检查是否符合websocket协议规范
	if err = checkHeader(r); err != nil {
		return nil, err
	}

	// 2.2 return response
	websocketKey := r.Header.Get(internal.SecWebSocketKeyPair.Key)
	if len(websocketKey) == 0 {
		return nil, internal.ErrHandShake
	}
	respWriter := NewResponseWriter()
	defer respWriter.Close()

	respWriter.AddHeader(internal.SecWebSocketAcceptPair.Key, internal.GetSecWebSocketAccept(websocketKey))
	if err = respWriter.Write(netConn); err != nil {
		return nil, err
	}

	// 2.3 构造websocket conn对象
	wsConn := &WsConn{
		conn:         netConn,
		bufReader:    reader,
		eventHandler: up.eventHandler,
		frame:        NewFrame(),
		config:       up.options.CreateConfig(),
		sm:           sm,
	}
	return wsConn, nil
}

func (up *Upgrade) hijack(w http.ResponseWriter) (net.Conn, *bufio.Reader, error) {
	hi, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, internal.ErrInternalServer
	}
	netConn, rw, err := hi.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return netConn, rw.Reader, nil
}

func checkHeader(r *http.Request) error {
	if r.Method != http.MethodGet {
		return internal.ErrHandShake
	}
	if r.Header.Get(internal.ConnectionPair.Key) != internal.ConnectionPair.Value {
		return internal.ErrHandShake
	}
	if r.Header.Get(internal.UpgradePair.Key) != internal.UpgradePair.Value {
		return internal.ErrHandShake
	}
	if r.Header.Get(internal.SecWebSocketVersionPair.Key) != internal.SecWebSocketVersionPair.Value {
		return internal.ErrHandShake
	}
	return nil
}

func initServerOptions(options *ServerOptions) {
	if options == nil {
		options = new(ServerOptions)
	}
	if options.ReaderBufSize <= 0 {
		options.ReaderBufSize = defaultReaderBufSize
	}
	if options.MaxReadPayloadSize <= 0 {
		options.MaxReadPayloadSize = defaultMaxReadPayloadSize
	}
	if options.MaxWritePayloadSize <= 0 {
		options.MaxWritePayloadSize = defaultMaxWritePayloadSize
	}

	if options.NewSessionMap == nil {
		options.NewSessionMap = func() SessionManager {
			return New[string, any](10, 128)
		}
	}
	if options.PreSessionHandle == nil {
		options.PreSessionHandle = func(r *http.Request, sm SessionManager) error {
			return nil
		}
	}

	options.readerBufPool = NewPool(func() *bufio.Reader {
		return bufio.NewReaderSize(nil, options.ReaderBufSize)
	})
}
