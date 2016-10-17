package obs

import (
	"fmt"
	"io"
	"obs/tracing"
	"os"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var traceHostname string

func init() {
	traceHostname, _ = os.Hostname()
}

func tracingUnaryClientInterceptor(fr FlightRecorder, tracer opentracing.Tracer) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		fs, ctx, done := fr.WithNewSpan(ctx, "grpc:"+method)
		defer done()
		span := fs.TraceSpan()
		ext.SpanKind.Set(span, ext.SpanKindRPCClientEnum)

		md, ok := metadata.FromContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		if err := tracer.Inject(span.Context(), opentracing.TextMap, grpcTraceMD(md)); err != nil {
			fs.Warn("tracer_inject", "error injecting trace metadata", Vals{}.WithError(err))
		}

		ctx = metadata.NewContext(ctx, md)

		if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
			fs.Info(fmt.Sprintf("error in gRPC %s", method), Vals{}.WithError(err))
			ext.Error.Set(span, true)
			return err
		}

		return nil
	}
}

func tracingStreamClientInterceptor(fr FlightRecorder, tracer opentracing.Tracer) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		fs, ctx, done := fr.WithNewSpan(ctx, "grpc:"+method)
		span := fs.TraceSpan()
		ext.SpanKind.Set(span, ext.SpanKindRPCClientEnum)

		md, ok := metadata.FromContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		if err := tracer.Inject(span.Context(), opentracing.TextMap, grpcTraceMD(md)); err != nil {
			fs.Warn("tracer_inject", "error injecting trace metadata", Vals{}.WithError(err))
		}

		ctx = metadata.NewContext(ctx, md)

		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			fs.Info(fmt.Sprintf("error in gRPC %s", method), Vals{}.WithError(err))
			ext.Error.Set(span, true)
		}

		return &clientStreamInterceptor{cs, span, done, 0, 0}, err
	}
}

func tracingUnaryServerInterceptor(fr FlightRecorder, tracer opentracing.Tracer) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		md, ok := metadata.FromContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}
		spanCtx, err := tracer.Extract(opentracing.TextMap, grpcTraceMD(md))

		fs, ctx, done := fr.WithNewSpanContext(ctx, "grpc:"+info.FullMethod, spanCtx)
		defer done()
		span := fs.TraceSpan()
		ext.SpanKind.Set(span, ext.SpanKindRPCServerEnum)
		span.SetTag("grpc.hostname", traceHostname)

		if err != nil && err != opentracing.ErrSpanContextNotFound {
			fs.Warn("tracer_extract", "error extracting trace metadata", Vals{}.WithError(err))
		}

		ctx = opentracing.ContextWithSpan(ctx, span)
		resp, err = handler(ctx, req)
		if err != nil {
			fs.Info(fmt.Sprintf("error in gRPC %s", info.FullMethod), Vals{}.WithError(err))
			ext.Error.Set(span, true)
			span.SetTag(tracing.Label.ErrorMessage, fmt.Sprintf("%v", err))
			return resp, err
		}
		return resp, nil
	}
}

func tracingStreamServerInterceptor(fr FlightRecorder, tracer opentracing.Tracer) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()
		md, ok := metadata.FromContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}
		spanCtx, err := tracer.Extract(opentracing.TextMap, grpcTraceMD(md))

		fs, ctx, done := fr.WithNewSpanContext(ctx, "grpc:"+info.FullMethod, spanCtx)
		span := fs.TraceSpan()
		ext.SpanKind.Set(span, ext.SpanKindRPCServerEnum)
		span.SetTag("grpc.hostname", traceHostname)

		if err != nil && err != opentracing.ErrSpanContextNotFound {
			fs.Warn("tracer_extract", "error extracting trace metadata", Vals{}.WithError(err))
		}

		ctx = opentracing.ContextWithSpan(ctx, span)
		ssi := &serverStreamInterceptor{ss, span, done, 0, 0, ctx}
		defer ssi.finish()
		if err := handler(srv, ssi); err != nil {
			fs.Info(fmt.Sprintf("error in gRPC %s", info.FullMethod), Vals{}.WithError(err))
			ext.Error.Set(span, true)
			span.SetTag(tracing.Label.ErrorMessage, fmt.Sprintf("%v", err))
			return err
		}
		return nil
	}
}

type clientStreamInterceptor struct {
	cs                grpc.ClientStream
	span              opentracing.Span
	done              func()
	inCount, outCount int
}

func (csi *clientStreamInterceptor) Header() (metadata.MD, error) {
	return csi.cs.Header()
}

func (csi *clientStreamInterceptor) Trailer() metadata.MD {
	return csi.cs.Trailer()
}

func (csi *clientStreamInterceptor) CloseSend() error {
	return csi.cs.CloseSend()
}

func (csi *clientStreamInterceptor) Context() context.Context {
	return csi.cs.Context()
}

func (csi *clientStreamInterceptor) SendMsg(m interface{}) error {
	csi.outCount++
	return csi.cs.SendMsg(m)
}
func (csi *clientStreamInterceptor) RecvMsg(m interface{}) error {
	err := csi.cs.RecvMsg(m)
	if err == io.EOF {
		csi.span.SetTag("grpc.stream_received", csi.inCount)
		csi.span.SetTag("grpc.stream_sent", csi.outCount)
		csi.done()
		return err
	}
	csi.inCount++

	return err
}

type serverStreamInterceptor struct {
	ss                grpc.ServerStream
	span              opentracing.Span
	done              func()
	inCount, outCount int
	ctx               context.Context
}

func (ssi *serverStreamInterceptor) SendHeader(md metadata.MD) error {
	return ssi.ss.SendHeader(md)
}

func (ssi *serverStreamInterceptor) SetTrailer(md metadata.MD) {
	ssi.ss.SetTrailer(md)
}

func (ssi *serverStreamInterceptor) Context() context.Context {
	return ssi.ctx
}

func (ssi *serverStreamInterceptor) SendMsg(m interface{}) error {
	ssi.outCount++
	return ssi.ss.SendMsg(m)
}

func (ssi *serverStreamInterceptor) RecvMsg(m interface{}) error {
	ssi.inCount++
	return ssi.ss.RecvMsg(m)
}

func (ssi *serverStreamInterceptor) finish() {
	ssi.span.SetTag("grpc.stream_received", ssi.inCount)
	ssi.span.SetTag("grpc.stream_sent", ssi.outCount)
	ssi.done()
}

type grpcTraceMD metadata.MD

func (g grpcTraceMD) Set(key, val string) {
	g[key] = append(g[key], val)
}

func (g grpcTraceMD) ForeachKey(handler func(key, val string) error) error {
	for k, vs := range g {
		for _, v := range vs {
			rk, rv, err := metadata.DecodeKeyValue(k, v)
			if err != nil {
				return err
			}
			if err = handler(rk, rv); err != nil {
				return err
			}
		}
	}

	return nil
}
