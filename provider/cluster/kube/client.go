package kube

import (
	"fmt"
	"path"

	"github.com/ovrclk/akash/provider/cluster"
	"github.com/ovrclk/akash/types"
	"github.com/tendermint/tmlibs/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Client interface {
	cluster.Client
}

type client struct {
	kc  kubernetes.Interface
	log log.Logger
}

func NewClient(log log.Logger) (Client, error) {

	kubeconfig := path.Join(homedir.HomeDir(), ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("error building config flags: %v", err)
	}

	kc, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating kubernetes client: %v", err)
	}

	_, err = kc.CoreV1().Namespaces().List(metav1.ListOptions{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("error connecting to kubernetes: %v", err)
	}

	return &client{
		kc:  kc,
		log: log,
	}, nil

}

func (c *client) Deployments() ([]cluster.Deployment, error) {

	// TODO: loop to more pages.
	namespaces, err := c.kc.CoreV1().Namespaces().List(metav1.ListOptions{
		LabelSelector: deploymentSelector().String(),
	})

	if err != nil {
		return nil, err
	}

	var deployments []cluster.Deployment

	for _, ns := range namespaces.Items {
		obj, err := deploymentFromAnnotation(ns.Annotations)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, obj)
	}

	return deployments, nil
}

func (c *client) Deploy(lid types.LeaseID, group *types.ManifestGroup) error {

	if err := applyNS(c.kc, newNSBuilder(lid, group)); err != nil {
		c.log.Error("applying namespace", "err", err, "lease", lid)
		return err
	}

	for _, service := range group.Services {
		if err := applyDeployment(c.kc, newDeploymentBuilder(lid, group, service)); err != nil {
			c.log.Error("applying deployment", "err", err, "lease", lid, "service", service.Name)
			return err
		}

		if len(service.Expose) == 0 {
			c.log.Debug("no services", "lease", lid, "service", service.Name)
			continue
		}

		if err := applyService(c.kc, newServiceBuilder(lid, group, service)); err != nil {
			c.log.Error("applying service", "err", err, "lease", lid, "service", service.Name)
			return err
		}

		for _, expose := range service.Expose {
			if !c.shouldExpose(expose) {
				continue
			}
			if err := applyIngress(c.kc, newIngressBuilder(lid, group, service, expose)); err != nil {
				c.log.Error("applying ingress", "err", err, "lease", lid, "service", service.Name, "expose", expose)
				return err
			}
		}
	}

	return nil
}

func (c *client) Teardown(lid types.LeaseID) error {
	return c.kc.CoreV1().Namespaces().Delete(lidNS(lid), &metav1.DeleteOptions{})
}

func (c *client) shouldExpose(expose *types.ManifestServiceExpose) bool {
	return expose.Global &&
		(expose.ExternalPort == 80 ||
			(expose.ExternalPort == 0 && expose.Port == 80))
}