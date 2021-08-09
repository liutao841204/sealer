package plugin

import (
	ctx "context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/snapshot"
	"go.uber.org/zap"

	"github.com/alibaba/sealer/logger"
	"github.com/alibaba/sealer/utils/ssh"
)

type EtcdBackupPlugin struct {
	name    string
	backDir string
}

func (e EtcdBackupPlugin) Run(context Context, phase Phase) error {
	masterIP, err := getMasterIP(context)
	if err != nil {
		return err
	}

	if err := fetchRemoteCert(context, masterIP); err != nil {
		return err
	}

	cfg, err := connEtcd(masterIP)
	if err != nil {
		return err
	}

	return snapshotEtcd(&e, cfg)
}

func getMasterIP(context Context) (string, error) {
	ipList := context.Cluster.Spec.Masters.IPList
	if len(ipList) == 0 {
		return "", errors.New("cluster master does not exist")
	}
	return ipList[0], nil
}

func fetchRemoteCert(context Context, masterIP string) error {
	SSH := ssh.NewSSHByCluster(context.Cluster)
	certs := []string{"healthcheck-client.crt", "healthcheck-client.key", "ca.crt"}
	for _, cert := range certs {
		if err := SSH.Fetch(masterIP, "/tmp/"+cert, "/etc/kubernetes/pki/etcd/"+cert); err != nil {
			logger.Error("host %s %s file does not exist, err: %v\n", masterIP, cert, err)
			return err
		}
	}
	return nil
}

func connEtcd(masterIP string) (clientv3.Config, error) {
	const dialTimeout = 5 * time.Second
	const etcdCert = "/tmp/healthcheck-client.crt"
	const etcdCertKey = "/tmp/healthcheck-client.key"
	const etcdCa = "/tmp/ca.crt"

	cert, err := tls.LoadX509KeyPair(etcdCert, etcdCertKey)
	if err != nil {
		logger.Error("cacert or key file is not exist, err:%v\n", err)
		return clientv3.Config{}, err
	}

	caData, err := ioutil.ReadFile(etcdCa)
	if err != nil {
		logger.Error("ca certificate reading failed, err:%v\n", err)
		return clientv3.Config{}, err
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caData)

	_tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
	}

	endpoints := []string{fmt.Sprintf("https://%s:2379", masterIP)}
	cfg := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
		TLS:         _tlsConfig,
	}

	cli, err := clientv3.New(cfg)
	if err != nil {
		logger.Error("connect to etcd failed, err:%v\n", err)
		return clientv3.Config{}, err
	}

	fmt.Println("connect to etcd success")

	defer cli.Close()

	return cfg, nil
}

func snapshotEtcd(e *EtcdBackupPlugin, cfg clientv3.Config) error {
	lg, err := zap.NewProduction()
	if err != nil {
		logger.Error("get lg error, err:%v\n", err)
		return err
	}

	ctx, cancel := ctx.WithCancel(ctx.Background())
	defer cancel()

	var dbPath = fmt.Sprintf("%s/%s", e.backDir, e.name)
	if err := snapshot.Save(ctx, lg, cfg, dbPath); err != nil {
		logger.Error("snapshot save err: %v\n", err)
		return err
	}
	logger.Info("Snapshot saved at %s\n", dbPath)

	return nil
}
