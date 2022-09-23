// Copyright 2022 TriggerMesh Inc.
// SPDX-License-Identifier: Apache-2.0

package redisbroker

import (
	"context"

	"go.uber.org/zap"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/client/injection/kube/informers/apps/v1/deployment"
	"knative.dev/pkg/client/injection/kube/informers/core/v1/service"
	"knative.dev/pkg/client/injection/kube/informers/core/v1/serviceaccount"
	"knative.dev/pkg/client/injection/kube/informers/rbac/v1/rolebinding"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"

	eventingv1alpha1 "github.com/triggermesh/triggermesh-core/pkg/apis/eventing/v1alpha1"
	rbinformer "github.com/triggermesh/triggermesh-core/pkg/client/generated/injection/informers/eventing/v1alpha1/redisbroker"
	trginformer "github.com/triggermesh/triggermesh-core/pkg/client/generated/injection/informers/eventing/v1alpha1/trigger"
	rbreconciler "github.com/triggermesh/triggermesh-core/pkg/client/generated/injection/reconciler/eventing/v1alpha1/redisbroker"
)

// NewController initializes the controller and is called by the generated code
// Registers event handlers to enqueue events
func NewController(
	ctx context.Context,
	cmw configmap.Watcher,
) *controller.Impl {

	rbInformer := rbinformer.Get(ctx)
	trgInformer := trginformer.Get(ctx)
	deploymentInformer := deployment.Get(ctx)
	serviceInformer := service.Get(ctx)
	serviceAccountInformer := serviceaccount.Get(ctx)
	_ = rolebinding.Get(ctx)

	r := &Reconciler{
		kubeClientSet:    kubeclient.Get(ctx),
		redisReconciler:  newRedisReconciler(ctx, deploymentInformer.Lister(), serviceInformer.Lister()),
		brokerReconciler: newBrokerReconciler(ctx, deploymentInformer.Lister(), serviceInformer.Lister()),
	}

	impl := rbreconciler.NewImpl(ctx, r)
	rb := &eventingv1alpha1.RedisBroker{}
	gvk := rb.GetGroupVersionKind()

	rbInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue))

	deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterController(rb),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})

	serviceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterController(rb),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})
	serviceAccountInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterController(rb),
		Handler:    controller.HandleAll(impl.EnqueueControllerOf),
	})

	filterTriggerForRedisBroker := func(obj interface{}) bool {
		t, ok := obj.(*eventingv1alpha1.Trigger)
		if !ok {
			return false
		}

		// TODO replace with defaulting when webhook is implemented
		if !(t.Spec.Broker.Group == gvk.Group || t.Spec.Broker.Group == "") ||
			t.Spec.Broker.Kind != gvk.Kind {
			return false
		}

		// TODO replace with broker namespace when webhook defaulting is implemented
		_, err := rbInformer.Lister().RedisBrokers(t.Namespace).Get(t.Spec.Broker.Name)
		switch {
		case err == nil:
			return true
		case !apierrs.IsNotFound(err):
			logging.FromContext(ctx).Error("Unable to get Redis Broker", zap.Any("broker", t.Spec.Broker), zap.Error(err))
		}

		return false

	}
	enqueueFromTrigger := func(obj interface{}) {
		t, ok := obj.(*eventingv1alpha1.Trigger)
		if !ok {
			return
		}

		impl.EnqueueKey(types.NamespacedName{
			Name:      t.Spec.Broker.Name,
			Namespace: t.Namespace,
		})
	}

	// Filter triggers for redisbroker
	// enqueue at the broker

	trgInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterTriggerForRedisBroker,
		Handler:    controller.HandleAll(enqueueFromTrigger),
	})

	return impl
}
