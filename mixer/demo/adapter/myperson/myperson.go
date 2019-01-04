package myperson

import (
	"context"
	"fmt"
	"net"

	google_rpc "github.com/gogo/googleapis/google/rpc"
	"google.golang.org/grpc"
	istio_mixer_adapter_model_v1beta11 "istio.io/api/mixer/adapter/model/v1beta1"
	"istio.io/istio/mixer/adapter/myperson/config"
	"istio.io/istio/mixer/template/person"
)

type (
	Server interface {
		Addr() string
		Close() error
		Run(shutdown chan error)
	}

	MyPerson struct {
		listener net.Listener
		server   *grpc.Server
	}
)

var _ person.HandlePersonServiceServer = &MyPerson{}

func (m *MyPerson) Addr() string {
	return m.listener.Addr().String()
}

func (m *MyPerson) Close() error {
	if m.server != nil {
		m.server.GracefulStop()
	}
	if m.listener != nil {
		m.listener.Close()
	}
	return nil
}

func (m *MyPerson) Run(shutdown chan error) {
	shutdown <- m.server.Serve(m.listener)
}

func (m *MyPerson) HandlePerson(ctx context.Context, req *person.HandlePersonRequest) (
	*istio_mixer_adapter_model_v1beta11.CheckResult, error) {
	fmt.Printf("print person request data: %s, %d, %s\n",
		req.Instance.Owner,
		req.Instance.Age,
		req.Instance.EmailAddress,
	)
	fmt.Println("print adapter info.....")
	cfg := &config.Params{}
	if err := cfg.Unmarshal(req.AdapterConfig.Value); err != nil {
		panic(err.Error())
	}
	fmt.Printf("print person adapter data: %s, %d, %s\n",
		cfg.Owner,
		cfg.Age,
		cfg.EmailAddress,
	)
	if req.Instance.Owner == cfg.Owner &&
		req.Instance.Age == cfg.Age &&
		req.Instance.EmailAddress == cfg.EmailAddress {
		return &istio_mixer_adapter_model_v1beta11.CheckResult{}, nil
	}
	return &istio_mixer_adapter_model_v1beta11.CheckResult{
		Status: google_rpc.Status{
			Code:    40001,
			Message: "基本信息不匹配",
		},
	}, nil
}

func NewMyPerson(addr string) (Server, error) {
	if addr == "" {
		addr = "127.0.0.1:4001"
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s", addr))
	if err != nil {
		return nil, err
	}
	s := &MyPerson{
		listener: listener,
	}
	fmt.Printf("grpc://%s\n", addr)
	s.server = grpc.NewServer()
	person.RegisterHandlePersonServiceServer(s.server, s)
	return s, nil
}
