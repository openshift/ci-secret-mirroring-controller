package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	testclient "k8s.io/client-go/kubernetes/fake"
	clientgo_testing "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestMirrorSecret(t *testing.T) {
	config := Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "test-ns", Name: "src"},
				To:   SecretLocation{Namespace: "test-ns", Name: "dst"},
			},
		},
	}
	defaultSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "src"},
		Data:       map[string][]byte{"test_key": []byte("test_value")},
	}
	for _, tc := range []struct {
		id                    string
		config                Configuration
		src                   v1.Secret
		shouldCopy, shouldErr bool
	}{
		{
			id:  "empty src is ignored",
			src: v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "src"}},
		},
		{
			id:         "normal secret is copied",
			src:        defaultSecret,
			shouldCopy: true,
		},
		{
			id:        "error is reported",
			src:       defaultSecret,
			shouldErr: true,
		},
	} {
		client := testclient.NewSimpleClientset()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		informers := informers.NewSharedInformerFactory(client, 5*time.Minute)
		informer := informers.Core().V1().Secrets()
		informer.Informer().AddEventHandler(&cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) { cancel() },
		})
		informers.Start(ctx.Done())
		secretClient := client.CoreV1().Secrets("test-ns")
		if _, err := secretClient.Create(&tc.src); err != nil {
			t.Errorf("%s: %s", tc.id, err)
			continue
		}
		<-ctx.Done()
		c := NewSecretMirror(informer, client, config)
		if tc.shouldErr {
			client.Fake.PrependReactor(
				"create", "secrets",
				func(clientgo_testing.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("injected error")
				})
		}
		if err := c.reconcile("test-ns/src"); err != nil != tc.shouldErr {
			t.Errorf("%s: shouldErr is %t, got %v", tc.id, tc.shouldErr, err)
			continue
		}
		if _, err := secretClient.Get("dst", metav1.GetOptions{}); err != nil {
			if tc.shouldCopy && !errors.IsNotFound(err) {
				t.Errorf("%s: %s", tc.id, err)
				continue
			}
		}
	}
}
