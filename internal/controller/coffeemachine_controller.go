/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	appliancesv1alpha1 "github.com/sijoma/home-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// CoffeeMachineReconciler reconciles a CoffeeMachine object
// Todo: Make use of EventRecorder
type CoffeeMachineReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	MqttClient mqtt.Client
}

// Todo: Would be great to have this configuration defined in a CRD or atleast env vars
const topicAttribute = "power"
const topicPrefix = "home/kitchen/coffee"

//+kubebuilder:rbac:groups=appliances.home.sijoma.dev,resources=coffeemachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=appliances.home.sijoma.dev,resources=coffeemachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=appliances.home.sijoma.dev,resources=coffeemachines/finalizers,verbs=update

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *CoffeeMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info(req.Name, "namespace", req.Namespace)

	coffeeMachine := &appliancesv1alpha1.CoffeeMachine{}
	err := r.Get(ctx, req.NamespacedName, coffeeMachine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("coffeeMachine resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get coffeeMachine")
		return ctrl.Result{}, err
	}

	// Publish message to machineTopic to turn coffee machine to desired state
	machineTopic := fmt.Sprintf("%s/%s/%s", topicPrefix, coffeeMachine.Name, topicAttribute)
	// Todo: Usually the power would be values of 0 or 1 when using mqtt in smart home scenarios
	token := r.MqttClient.Publish(machineTopic, 1, false, fmt.Sprintf("%t", coffeeMachine.Spec.Power))
	// We should not block to long
	token.WaitTimeout(time.Second)
	err = token.Error()
	if err != nil {
		log.Error(err, "could not publish message")
		if err.Error() == "not Connected" {
			// Hack to reconnect to mqtt.
			os.Exit(1)
		}
		return ctrl.Result{}, err
	}
	log.Info(
		"published message",
		"coffee machine", coffeeMachine.Name,
		"power state", coffeeMachine.Spec.Power,
	)

	isPoweredOn := metav1.ConditionTrue
	if coffeeMachine.Spec.Power == false {
		isPoweredOn = metav1.ConditionFalse
	}

	// Todo: Would be good to follow api conventions here
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties
	meta.SetStatusCondition(&coffeeMachine.Status.Conditions, metav1.Condition{
		Type:    "poweredOn",
		Status:  isPoweredOn,
		Message: fmt.Sprintf("Coffee Machine %s is powered %t", coffeeMachine.Name, coffeeMachine.Spec.Power),
		Reason:  "Updated",
	})

	// We blindly set this as we expect the coffee machine to respond to the MQTT message if it was successfully sent
	coffeeMachine.Status.ObservedPower = coffeeMachine.Spec.Power
	if err := r.Status().Update(ctx, coffeeMachine); err != nil {
		log.Error(err, "Failed to update CoffeeMachine Status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

var subscription chan mqtt.Message

// SetupWithManager sets up the controller with the Manager.
func (r *CoffeeMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	coffeeMachineTopic := fmt.Sprintf("%s/%s/%s", topicPrefix, "+", topicAttribute)
	token := r.MqttClient.Subscribe(coffeeMachineTopic, 1, messageCallback)
	token.Wait()

	subscription = make(chan mqtt.Message)
	eventChannel := make(chan event.GenericEvent)
	coffeeMachineEvent := CreateCoffeeMachineEvents(mgr.GetClient(), subscription, eventChannel)
	go coffeeMachineEvent.Run()

	return ctrl.NewControllerManagedBy(mgr).
		For(&appliancesv1alpha1.CoffeeMachine{}).
		Watches(&source.Channel{Source: eventChannel}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

func messageCallback(_ mqtt.Client, message mqtt.Message) {
	subscription <- message
}
