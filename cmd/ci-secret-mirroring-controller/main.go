package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/ci-secret-mirroring-controller/pkg/controller"
	"github.com/openshift/ci-secret-mirroring-controller/pkg/controller/config"
)

const (
	resync = 5 * time.Minute
)

type options struct {
	configLocation string
	numWorkers     int
	logLevel       string
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}
	flag.StringVar(&opt.configLocation, "config", "", "Path to configuration file.")
	flag.IntVar(&opt.numWorkers, "num-workers", 10, "Number of worker threads.")
	flag.StringVar(&opt.logLevel, "log-level", logrus.DebugLevel.String(), "Logging level.")

	return opt
}

func (o *options) Validate() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("failed to parse --log-level: %v", err)
	}
	logrus.SetLevel(level)

	if o.numWorkers < 1 {
		return fmt.Errorf("a non-zero, positive --num-workers is necessary, not %d", o.numWorkers)
	}

	if o.configLocation == "" {
		return errors.New("a file path must be provided for --config")
	}

	return nil
}

func (o *options) Run() error {
	configAgent := &config.Agent{}
	if err := configAgent.Start(o.configLocation); err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	defer configAgent.Stop()

	clusterConfig, err := loadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load cluster config")
	}

	client, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to initialize kubernetes client")
	}

	informerFactory := informers.NewSharedInformerFactory(client, resync)

	secretMirror := controller.NewSecretMirror(informerFactory.Core().V1().Secrets(), client, configAgent.Config)
	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		close(stop)
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()
	defer close(stop)
	go informerFactory.Start(stop)
	go secretMirror.Run(o.numWorkers, stop)

	// Wait forever
	select {}
}

// loadClusterConfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func loadClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}

	credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("could not load credentials from config: %v", err)
	}

	clusterConfig, err = clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

func main() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	flagSet.Parse(os.Args[1:])

	if err := opt.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options specified.")
	}

	if err := opt.Run(); err != nil {
		logrus.WithError(err).Fatal("Failed to run secret mirroring controller")
	}
}
