package controller

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	kubeclientset "k8s.io/client-go/kubernetes"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"reflect"
)

const (
	// maxRetries is the number of times a service will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the
	// sequence of delays between successive queuings of a service.
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15

	secretMirrorname = "secret-mirroring-manager"
)

// NewSecretMirror returns a new *SecretMirror to generate deletion requests.
func NewSecretMirror(informer coreinformers.SecretInformer, client kubeclientset.Interface, config Configuration) *SecretMirror {
	logger := logrus.WithField("controller", secretMirrorname)
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logger.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclient.EventSinkImpl{Interface: coreclient.New(client.CoreV1().RESTClient()).Events("")})

	c := &SecretMirror{
		config: config,
		client: client,
		queue:  workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), secretMirrorname),
		logger: logger,
		lister: informer.Lister(),
		synced: informer.Informer().HasSynced,
	}

	informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.add,
		UpdateFunc: c.update,
	})

	return c
}

// SecretMirror manages deletion requests for namespaces.
type SecretMirror struct {
	config Configuration
	client kubeclientset.Interface

	lister corelisters.SecretLister
	queue  workqueue.RateLimitingInterface
	synced cache.InformerSynced

	logger *logrus.Entry
}

func (c *SecretMirror) add(obj interface{}) {
	secret := obj.(*coreapi.Secret)
	c.logger.Debugf("enqueueing added secret %s/%s", secret.GetNamespace(), secret.GetName())
	c.enqueue(secret)
}

func (c *SecretMirror) update(old, obj interface{}) {
	secret := obj.(*coreapi.Secret)
	c.logger.Debugf("enqueueing updated secret %s/%s", secret.GetNamespace(), secret.GetName())
	c.enqueue(secret)
}

// Run runs c; will not return until stopCh is closed. workers determines how
// many clusters will be handled in parallel.
func (c *SecretMirror) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	c.logger.Infof("starting %s controller", secretMirrorname)
	defer c.logger.Infof("shutting down %s controller", secretMirrorname)

	c.logger.Infof("Waiting for caches to reconcile for %s controller", secretMirrorname)
	if !cache.WaitForCacheSync(stopCh, c.synced) {
		utilruntime.HandleError(fmt.Errorf("unable to reconcile caches for %s controller", secretMirrorname))
	}
	c.logger.Infof("Caches are synced for %s controller", secretMirrorname)

	for i := 0; i < workers; i++ {
		go wait.Until(c.worker, time.Second, stopCh)
	}

	<-stopCh
}

func (c *SecretMirror) enqueue(obj metav1.Object) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %#v: %v", obj, err))
		return
	}

	c.queue.Add(key)
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (c *SecretMirror) worker() {
	for c.processNextWorkItem() {
	}
}

func (c *SecretMirror) processNextWorkItem() bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	err := c.reconcile(key.(string))
	c.handleErr(err, key)

	return true
}

func (c *SecretMirror) handleErr(err error, key interface{}) {
	if err == nil {
		c.queue.Forget(key)
		return
	}

	logger := c.logger.WithField("secret", key)

	logger.Errorf("error syncing secret: %v", err)
	if c.queue.NumRequeues(key) < maxRetries {
		logger.Errorf("retrying secret")
		c.queue.AddRateLimited(key)
		return
	}

	utilruntime.HandleError(err)
	logger.Infof("dropping secret out of the queue: %v", err)
	c.queue.Forget(key)
}

// reconcile handles the business logic of ensuring that namespaces
// are reaped when they are past their hard or soft TTLs
func (c *SecretMirror) reconcile(key string) error {
	logger := c.logger.WithField("key", key)
	logger.Infof("reconciling secret")
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	logger = logger.WithFields(logrus.Fields{
		"source-namespace": namespace, "source-secret": name,
	})

	source, err := c.lister.Secrets(namespace).Get(name)
	if errors.IsNotFound(err) {
		logger.Info("not doing work for secret because it has been deleted")
		return nil
	}
	if err != nil {
		logger.WithError(err).Errorf("unable to retrieve secret from store")
		return err
	}
	if !source.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("not doing work for secret because it is being deleted")
		return nil
	}

	var mirrorErrors []error
	for _, mirrorConfig := range c.config.Secrets {
		if mirrorConfig.From.Namespace == namespace && mirrorConfig.From.Name == name {
			mirrorErrors = append(mirrorErrors, c.mirrorSecret(source, mirrorConfig.To, logger))
		}
	}

	logger.Info("finished handling secret")
	if len(mirrorErrors) > 0 {
		return fmt.Errorf("failed to mirror secret: %v", mirrorErrors)
	}
	return nil
}

func (c *SecretMirror) mirrorSecret(source *coreapi.Secret, to SecretLocation, logger *logrus.Entry) error {
	logger = logger.WithFields(logrus.Fields{
		"target-namespace": to.Namespace, "target-secret": to.Name},
	)
	logger.Info("processing mirror request")

	if len(source.Data) == 0 {
		logger.Info("not updating target secret as source has no data")
		return nil
	}

	if secret, getErr := c.lister.Secrets(to.Namespace).Get(to.Name); getErr == nil {
		if reflect.DeepEqual(secret.Data, source.Data) {
			logger.Info("not updating target secret as it already matches the source")
			return nil
		}
		logger.Info("updating target secret")
		destination := secret.DeepCopy()
		destination.Data = source.Data
		_, updateErr := c.client.CoreV1().Secrets(to.Namespace).Update(destination)
		return updateErr
	} else if errors.IsNotFound(getErr) {
		logger.Info("creating target secret")
		destination := &coreapi.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      to.Name,
				Namespace: to.Namespace,
			},
			Data: source.Data,
		}
		_, createErr := c.client.CoreV1().Secrets(to.Namespace).Create(destination)
		return createErr
	} else {
		return getErr
	}
}
