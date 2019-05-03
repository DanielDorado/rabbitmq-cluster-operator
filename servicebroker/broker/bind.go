package broker

import (
	"context"
	"fmt"

	"servicebroker/utils/binding"
	"servicebroker/utils/rabbithutch"

	rabbithole "github.com/michaelklishin/rabbit-hole"
	"github.com/pivotal-cf/brokerapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func createKubernetesClient() (*kubernetes.Clientset, error) {
	config, err := clientsetConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

func (broker RabbitMQServiceBroker) Bind(ctx context.Context, instanceID, bindingID string, details brokerapi.BindDetails, asyncAllowed bool) (brokerapi.Binding, error) {
	vhost := instanceID
	username := bindingID

	kubernetesClient, err := createKubernetesClient()
	if err != nil {
		return brokerapi.Binding{}, fmt.Errorf("Failed to create kubernetes client: %s", err)
	}

	getOptions := metav1.GetOptions{}
	service, err := kubernetesClient.CoreV1().Services("rabbitmq-for-kubernetes").Get(fmt.Sprintf("p-%s-rabbitmq", instanceID), getOptions)
	if err != nil {
		return brokerapi.Binding{}, fmt.Errorf("Failed to retrieve service: %s", err)
	}

	var serviceIP string
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		serviceIP = service.Status.LoadBalancer.Ingress[0].IP
	} else {
		return brokerapi.Binding{}, fmt.Errorf("Failed to retrieve service IP for %s", service.Name)
	}

	client, err := rabbithole.NewClient(
		fmt.Sprintf("http://%s:15672", serviceIP),
		broker.Config.RabbitMQ.Administrator.Username,
		broker.Config.RabbitMQ.Administrator.Password,
	)
	if err != nil {
		return brokerapi.Binding{}, err
	}

	rabbit := rabbithutch.New(client)

	protocolsPorts, err := rabbit.ProtocolPorts()
	if err != nil {
		return brokerapi.Binding{}, err
	}

	ok, err := rabbit.VHostExists(vhost)
	if err != nil {
		return brokerapi.Binding{}, err
	}

	if !ok {
		err = rabbit.VHostCreate(vhost)
		if err != nil {
			return brokerapi.Binding{}, err
		}
	}

	password, err := rabbit.CreateUserAndGrantPermissions(username, vhost, broker.Config.RabbitMQ.RegularUserTags)
	if err != nil {
		return brokerapi.Binding{}, err
	}

	credsBuilder := binding.Builder{
		MgmtDomain:    fmt.Sprintf("%s:%d", serviceIP, 15672),
		Hostnames:     []string{serviceIP},
		VHost:         vhost,
		Username:      username,
		Password:      password,
		TLS:           bool(broker.Config.RabbitMQ.TLS),
		ProtocolPorts: protocolsPorts,
	}

	credentials, err := credsBuilder.Build()
	if err != nil {
		return brokerapi.Binding{}, err
	}

	return brokerapi.Binding{Credentials: credentials}, nil
}
