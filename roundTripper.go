package requests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"net/http"

	"github.com/sky8282/gtls"
	"github.com/sky8282/http2"
	"github.com/sky8282/http3"
	"github.com/sky8282/ja3"
	"github.com/sky8282/tools"
	"github.com/quic-go/quic-go"
	uquic "github.com/refraction-networking/uquic"
)

type reqTask struct {
	ctx       context.Context
	cnl       context.CancelFunc
	reqCtx    *Response
	emptyPool chan struct{}
	err       error
	retry     int
}

func (obj *reqTask) suppertRetry() bool {
	if obj.reqCtx.request.Body == nil {
		return true
	} else if body, ok := obj.reqCtx.request.Body.(io.Seeker); ok {
		if i, err := body.Seek(0, io.SeekStart); i == 0 && err == nil {
			return true
		}
	}
	return false
}
func getKey(ctx *Response) (key string) {
	adds := []string{}
	for _, p := range ctx.proxys {
		adds = append(adds, getAddr(p))
	}
	adds = append(adds, getAddr(ctx.Request().URL))
	return strings.Join(adds, "@")
}

type roundTripper struct {
	ctx       context.Context
	cnl       context.CancelFunc
	connPools *connPools
	dialer    *Dialer
}

func newRoundTripper(preCtx context.Context) *roundTripper {
	if preCtx == nil {
		preCtx = context.TODO()
	}
	ctx, cnl := context.WithCancel(preCtx)
	return &roundTripper{
		ctx:       ctx,
		cnl:       cnl,
		dialer:    &Dialer{},
		connPools: newConnPools(),
	}
}
func (obj *roundTripper) newConnPool(done chan struct{}, conn *connecotr, key string) *connPool {
	pool := new(connPool)
	pool.connKey = key
	pool.forceCtx, pool.forceCnl = context.WithCancelCause(obj.ctx)
	pool.safeCtx, pool.safeCnl = context.WithCancelCause(pool.forceCtx)
	pool.tasks = make(chan *reqTask)

	pool.connPools = obj.connPools
	pool.total.Add(1)
	go pool.rwMain(done, conn)
	return pool
}
func (obj *roundTripper) putConnPool(key string, conn *connecotr) {
	pool := obj.connPools.get(key)
	done := make(chan struct{})
	if pool != nil {
		pool.total.Add(1)
		go pool.rwMain(done, conn)
	} else {
		obj.connPools.set(key, obj.newConnPool(done, conn, key))
	}
	<-done
}
func (obj *roundTripper) newConnecotr() *connecotr {
	conne := new(connecotr)
	conne.withCancel(obj.ctx, obj.ctx)
	return conne
}

func (obj *roundTripper) http3Dial(ctx *Response, remtoeAddress Address, proxyAddress ...Address) (udpConn net.PacketConn, err error) {
	if len(proxyAddress) > 0 {
		if proxyAddress[len(proxyAddress)-1].Scheme != "socks5" {
			err = errors.New("http3 last proxy must socks5 proxy")
			return
		}
		udpConn, _, err = obj.dialer.DialProxyContext(ctx, "tcp", ctx.option.TlsConfig.Clone(), append(proxyAddress, remtoeAddress)...)
	} else {
		udpConn, err = net.ListenUDP("udp", nil)
	}
	return
}
func (obj *roundTripper) ghttp3Dial(ctx *Response, remoteAddress Address, proxyAddress ...Address) (conn *connecotr, err error) {
	udpConn, err := obj.http3Dial(ctx, remoteAddress, proxyAddress...)
	if err != nil {
		return nil, err
	}
	tlsConfig := ctx.option.TlsConfig.Clone()
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	tlsConfig.ServerName = remoteAddress.Host
	if remoteAddress.IP == nil {
		remoteAddress.IP, err = obj.dialer.loadHost(ctx, remoteAddress.Name)
		if err != nil {
			return nil, err
		}
	}
	var quicConfig *quic.Config
	if ctx.option.UquicConfig != nil {
		quicConfig = ctx.option.QuicConfig.Clone()
	}
	netConn, err := quic.DialEarly(ctx.Context(), udpConn, &net.UDPAddr{IP: remoteAddress.IP, Port: remoteAddress.Port}, tlsConfig, quicConfig)
	conn = obj.newConnecotr()
	conn.Conn = http3.NewClient(netConn, func() {
		conn.forceCnl(errors.New("http3 client close"))
	})
	return
}

func (obj *roundTripper) uhttp3Dial(ctx *Response, remoteAddress Address, proxyAddress ...Address) (conn *connecotr, err error) {
	spec, err := ja3.CreateSpecWithUSpec(ctx.option.UJa3Spec)
	if err != nil {
		return nil, err
	}
	udpConn, err := obj.http3Dial(ctx, remoteAddress, proxyAddress...)
	if err != nil {
		return nil, err
	}
	tlsConfig := ctx.option.UtlsConfig.Clone()
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	tlsConfig.ServerName = remoteAddress.Host
	if remoteAddress.IP == nil {
		remoteAddress.IP, err = obj.dialer.loadHost(ctx, remoteAddress.Name)
		if err != nil {
			return nil, err
		}
	}
	var quicConfig *uquic.Config
	if ctx.option.UquicConfig != nil {
		quicConfig = ctx.option.UquicConfig.Clone()
	}
	netConn, err := (&uquic.UTransport{
		Transport: &uquic.Transport{
			Conn: udpConn,
		},
		QUICSpec: &spec,
	}).DialEarly(ctx.Context(), &net.UDPAddr{IP: remoteAddress.IP, Port: remoteAddress.Port}, tlsConfig, quicConfig)
	conn = obj.newConnecotr()
	conn.Conn = http3.NewUClient(netConn, func() {
		conn.forceCnl(errors.New("http3 client close"))
	})
	return
}

func (obj *roundTripper) dial(ctx *Response) (conn *connecotr, err error) {
	proxys, err := obj.initProxys(ctx)
	if err != nil {
		return nil, err
	}
	remoteAddress, err := GetAddressWithUrl(ctx.request.URL)
	if err != nil {
		return nil, err
	}
	if ctx.option.H3 {
		if ctx.option.UJa3Spec.IsSet() {
			return obj.uhttp3Dial(ctx, remoteAddress, proxys...)
		} else {
			return obj.ghttp3Dial(ctx, remoteAddress, proxys...)
		}
	}
	var netConn net.Conn
	if len(proxys) > 0 {
		_, netConn, err = obj.dialer.DialProxyContext(ctx, "tcp", ctx.option.TlsConfig.Clone(), append(proxys, remoteAddress)...)
	} else {
		var remoteAddress Address
		remoteAddress, err = GetAddressWithUrl(ctx.request.URL)
		if err != nil {
			return nil, err
		}
		netConn, err = obj.dialer.DialContext(ctx, "tcp", remoteAddress)
	}
	defer func() {
		if err != nil && netConn != nil {
			netConn.Close()
		}
	}()
	if err != nil {
		return nil, err
	}
	var h2 bool
	if ctx.request.URL.Scheme == "https" {
		netConn, h2, err = obj.dialAddTls(ctx.option, ctx.request, netConn)
		if ctx.option.Logger != nil {
			ctx.option.Logger(Log{
				Id:   ctx.requestId,
				Time: time.Now(),
				Type: LogType_TLSHandshake,
				Msg:  fmt.Sprintf("host:%s,  h2:%t", getHost(ctx.request), h2),
			})
		}
		if err != nil {
			return nil, err
		}
	}
	conne := obj.newConnecotr()
	conne.proxys = proxys
	conne.c = netConn
	err = obj.dialConnecotr(ctx.option, ctx.request, conne, h2)
	if err != nil {
		return nil, err
	}
	return conne, err
}
func (obj *roundTripper) dialConnecotr(option *RequestOption, req *http.Request, conne *connecotr, h2 bool) (err error) {
	if h2 {
		if option.H2Ja3Spec.OrderHeaders != nil {
			option.OrderHeaders = option.H2Ja3Spec.OrderHeaders
		}
		if conne.Conn, err = http2.NewClientConn(req.Context(), conne.c, option.H2Ja3Spec, func() {
			conne.forceCnl(errors.New("http2 client close"))
		}); err != nil {
			return err
		}
	} else {
		conne.Conn = newConn(conne.forceCtx, conne.c, func() {
			conne.forceCnl(errors.New("http1 client close"))
		})
	}
	return err
}
func (obj *roundTripper) dialAddTls(option *RequestOption, req *http.Request, netConn net.Conn) (net.Conn, bool, error) {
	ctx, cnl := context.WithTimeout(req.Context(), option.TlsHandshakeTimeout)
	defer cnl()
	if option.Ja3Spec.IsSet() {
		if tlsConn, err := obj.dialer.addJa3Tls(ctx, netConn, getHost(req), !option.ForceHttp1, option.Ja3Spec, option.UtlsConfig.Clone()); err != nil {
			return tlsConn, false, tools.WrapError(err, "add ja3 tls error")
		} else {
			return tlsConn, tlsConn.ConnectionState().NegotiatedProtocol == "h2", nil
		}
	} else {
		if tlsConn, err := obj.dialer.addTls(ctx, netConn, getHost(req), !option.ForceHttp1, option.TlsConfig.Clone()); err != nil {
			return tlsConn, false, tools.WrapError(err, "add tls error")
		} else {
			return tlsConn, tlsConn.ConnectionState().NegotiatedProtocol == "h2", nil
		}
	}
}
func (obj *roundTripper) initProxys(ctx *Response) ([]Address, error) {
	var proxys []Address
	if ctx.option.DisProxy {
		return nil, nil
	}
	if len(ctx.proxys) > 0 {
		proxys = make([]Address, len(ctx.proxys))
		for i, proxy := range ctx.proxys {
			proxyAddress, err := GetAddressWithUrl(proxy)
			if err != nil {
				return nil, err
			}
			proxys[i] = proxyAddress
		}
	}
	if len(proxys) == 0 && ctx.option.GetProxy != nil {
		proxyStr, err := ctx.option.GetProxy(ctx)
		if err != nil {
			return proxys, err
		}
		if proxyStr != "" {
			proxy, err := gtls.VerifyProxy(proxyStr)
			if err != nil {
				return proxys, err
			}
			proxyAddress, err := GetAddressWithUrl(proxy)
			if err != nil {
				return nil, err
			}
			proxys = []Address{proxyAddress}
		}
	}
	if len(proxys) == 0 && ctx.option.GetProxys != nil {
		proxyStrs, err := ctx.option.GetProxys(ctx)
		if err != nil {
			return proxys, err
		}
		if l := len(proxyStrs); l > 0 {
			proxys = make([]Address, l)
			for i, proxyStr := range proxyStrs {
				proxy, err := gtls.VerifyProxy(proxyStr)
				if err != nil {
					return proxys, err
				}
				proxyAddress, err := GetAddressWithUrl(proxy)
				if err != nil {
					return nil, err
				}
				proxys[i] = proxyAddress
			}
		}
	}
	return proxys, nil
}

func (obj *roundTripper) poolRoundTrip(pool *connPool, task *reqTask, key string) (isOk bool, err error) {
	task.ctx, task.cnl = context.WithTimeout(task.reqCtx.Context(), task.reqCtx.option.ResponseHeaderTimeout)
	select {
	case pool.tasks <- task:
		select {
		case <-task.emptyPool:
			return false, nil
		case <-task.ctx.Done():
			if task.err == nil && task.reqCtx.response == nil {
				task.err = context.Cause(task.ctx)
			}
			return true, task.err
		}
	default:
		return obj.createPool(task, key)
	}
}

func (obj *roundTripper) createPool(task *reqTask, key string) (isOk bool, err error) {
	task.reqCtx.isNewConn = true
	conn, err := obj.dial(task.reqCtx)
	if err != nil {
		if task.reqCtx.option.ErrCallBack != nil {
			task.reqCtx.err = err
			if err2 := task.reqCtx.option.ErrCallBack(task.reqCtx); err2 != nil {
				return true, err2
			}
		}
		return false, err
	}
	obj.putConnPool(key, conn)
	return false, nil
}

func (obj *roundTripper) closeConns() {
	for key, pool := range obj.connPools.Range() {
		pool.safeClose()
		obj.connPools.del(key)
	}
}
func (obj *roundTripper) forceCloseConns() {
	for key, pool := range obj.connPools.Range() {
		pool.forceClose()
		obj.connPools.del(key)
	}
}
func (obj *roundTripper) newReqTask(ctx *Response) *reqTask {
	if ctx.option.ResponseHeaderTimeout == 0 {
		ctx.option.ResponseHeaderTimeout = time.Second * 300
	}
	task := new(reqTask)
	task.reqCtx = ctx
	task.emptyPool = make(chan struct{})
	return task
}
func (obj *roundTripper) RoundTrip(ctx *Response) (err error) {
	if ctx.option.RequestCallBack != nil {
		if err = ctx.option.RequestCallBack(ctx); err != nil {
			if err == http.ErrUseLastResponse {
				if ctx.response == nil {
					return errors.New("errUseLastResponse response is nil")
				} else {
					return nil
				}
			}
			return err
		}
	}
	key := getKey(ctx) //pool key
	task := obj.newReqTask(ctx)
	var isOk bool
	for {
		select {
		case <-ctx.Context().Done():
			return context.Cause(ctx.Context())
		default:
		}
		if task.retry >= maxRetryCount {
			task.err = fmt.Errorf("roundTrip retry %d times", maxRetryCount)
			break
		}
		pool := obj.connPools.get(key)
		if pool == nil {
			isOk, err = obj.createPool(task, key)
		} else {
			isOk, err = obj.poolRoundTrip(pool, task, key)
		}
		if isOk {
			if err != nil {
				task.err = err
			}
			break
		}
		if err != nil {
			task.retry++
		}
	}
	if task.err == nil && ctx.option.RequestCallBack != nil {
		if err = ctx.option.RequestCallBack(ctx); err != nil {
			task.err = err
		}
	}
	return task.err
}

