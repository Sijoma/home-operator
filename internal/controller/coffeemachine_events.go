package controller

import (
	"context"
	"reflect"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-logr/logr"
	"github.com/sijoma/home-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type CoffeeMachineEvents struct {
	ctx            context.Context
	client         client.Client
	log            logr.Logger
	messages       <-chan mqtt.Message
	coffeeMachines chan<- event.GenericEvent
}

func CreateCoffeeMachineEvents(client client.Client, messages <-chan mqtt.Message, coffeeMachines chan<- event.GenericEvent) CoffeeMachineEvents {
	log := ctrl.Log.
		WithName("source").
		WithName(reflect.TypeOf(v1alpha1.CoffeeMachine{}).Name())
	return CoffeeMachineEvents{
		ctx:            context.Background(),
		client:         client,
		log:            log,
		messages:       messages,
		coffeeMachines: coffeeMachines,
	}
}

func (t *CoffeeMachineEvents) Run() {
	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		err := t.subscribe()
		if err != nil {
			t.log.Error(err, "error subscribe event")
		}
	}
}

func (t *CoffeeMachineEvents) subscribe() error {
	msg := <-t.messages
	log := t.log.WithValues(
		"messageId", msg.MessageID(),
		"topic", msg.Topic(),
	)

	machineName := strings.Split(msg.Topic(), "/")[3]
	var powerState bool
	if err := json.Unmarshal(msg.Payload(), &powerState); err != nil {
		log.Error(err, "unable to unmarshal event")
		return err
	}

	found := v1alpha1.CoffeeMachine{}
	objectKey := types.NamespacedName{
		Name: machineName,
		// Todo: Implement namespace, this would be the room e.g. "home/<namespace>/coffee/<id>/attribute"
		Namespace: "default",
	}
	if err := t.client.Get(context.Background(), objectKey, &found); err != nil {
		log.Error(err, "unable to get CoffeeMachines")
		return err
	}

	// Filter all where there is a difference in state
	if found.Status.ObservedPower != powerState {
		evt := event.GenericEvent{
			Object: &found,
		}
		t.coffeeMachines <- evt
	}
	msg.Ack()
	return nil
}
