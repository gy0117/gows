package gosocket

import (
	"bufio"
	"errors"
	"github.com/gy/gosocket/internal/cmap"
	"github.com/gy/gosocket/internal/pool"
	"github.com/gy/gosocket/internal/tools"
	"github.com/gy/gosocket/internal/types"
	"github.com/gy/gosocket/internal/xerr"
	"net"
	"net/http"
	"strings"
)

type HookFunc interface {
}

type Upgrade struct {
	options      *ServerOptions // Upgrade与ServerOptions绑定
	eventHandler EventHandler
}

func NewUpgrade(eventHandler EventHandler, options *ServerOptions) *Upgrade {
	return &Upgrade{
		eventHandler: eventHandler,
		options:      initServerOptions(options),
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
	websocketKey := r.Header.Get(types.SecWebSocketKeyPair.Key)
	if len(websocketKey) == 0 {
		return nil, xerr.NewError(xerr.ErrHandShake, errors.New("hand shake failed, websocketKey is nil"))
	}

	respWriter := NewResponseWriter()
	defer respWriter.Close()

	respWriter.AddHeader(types.SecWebSocketAcceptPair.Key, tools.GetSecWebSocketAccept(websocketKey))

	extensions := r.Header.Get(types.SecWebSocketExtensionsPair.Key)
	enableCompress := parseExtensions(extensions)
	if enableCompress {
		respWriter.AddHeader(types.SecWebSocketExtensionsPair.Key, tools.GetSecWebSocketExtensions())
	}

	if err = respWriter.Write(netConn); err != nil {
		return nil, err
	}

	// 2.3 构造websocket conn对象
	wsConn := &WsConn{
		conn:           netConn,
		bufReader:      reader,
		eventHandler:   up.eventHandler,
		frame:          NewFrame(),
		config:         up.options.CreateConfig(),
		sm:             sm,
		server:         true,
		enableCompress: enableCompress,
	}

	wsConn.Recycle = func() {
		wsConn.bufReader.Reset(nil)
		up.options.readerBufPool.Put(wsConn.bufReader)
		wsConn.bufReader = nil
	}
	return wsConn, nil
}

func parseExtensions(extensions string) bool {
	splits := strings.Split(extensions, ";")
	for _, s := range splits {
		if s == types.PermessageDeflate {
			return true
		}
	}
	return false
}

func (up *Upgrade) hijack(w http.ResponseWriter) (net.Conn, *bufio.Reader, error) {
	hi, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, xerr.NewError(xerr.ErrInternalServer, errors.New("hijacker failed"))
	}
	netConn, rw, err := hi.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return netConn, rw.Reader, nil
}

func checkHeader(r *http.Request) error {
	if r.Method != http.MethodGet {
		return xerr.NewError(xerr.ErrHandShake, errors.New("hand shake failed, method is not GET"))
	}
	if r.Header.Get(types.ConnectionPair.Key) != types.ConnectionPair.Value {
		return xerr.NewError(xerr.ErrHandShake, errors.New("hand shake failed, header connection error"))
	}
	if r.Header.Get(types.UpgradePair.Key) != types.UpgradePair.Value {
		return xerr.NewError(xerr.ErrHandShake, errors.New("hand shake failed, header upgrade error"))
	}
	if r.Header.Get(types.SecWebSocketVersionPair.Key) != types.SecWebSocketVersionPair.Value {
		return xerr.NewError(xerr.ErrHandShake, errors.New("hand shake failed, header Sec-WebSocket-Version error"))
	}
	return nil
}

func initServerOptions(options *ServerOptions) *ServerOptions {
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
			return cmap.New[string, any](10, 128)
		}
	}
	if options.PreSessionHandle == nil {
		options.PreSessionHandle = func(r *http.Request, sm SessionManager) error {
			return nil
		}
	}

	options.readerBufPool = pool.NewPool(func() *bufio.Reader {
		return bufio.NewReaderSize(nil, options.ReaderBufSize)
	})
	return options
}
