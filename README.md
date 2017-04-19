# Scope Network Control Plugin

The Scope Network Control plugin allows to modify the performance parameters of container's network interfaces using [Weave Scope](https://github.com/weaveworks/scope).
The following images show a simple example of how **status** and **controls** are displayed in scope UI.

<img src="imgs/network-control.png" width="1024
" alt="Scope Probe plugin screenshot" align="center">

## How to Run Scope Network Control Plugin

The Scope Network Control plugin can be executed stand alone.
It will respond to `GET /report` request on the `/var/run/scope/plugins/network-control/network-control.sock` in a JSON format.
If the running plugin has been registered by Scope, you will see it in the list of `PLUGINS` in the bottom right of the UI (see the green rectangle in the above figure).

**Note**: This plugin requires the `sch_netem` kernel module.

### Using a pre-built Docker image

If you want to make sure of running the latest available version of the plugin, you can pull the image from docker hub.

```
docker pull billyzhang2010/scope-network-control:latest
```

To run the Scope Network Control plugin you just need to run the following command.

```
docker run --rm -it \
			 --net=host --pid=host --privileged \
			 -v /var/run:/var/run \
			 --name envi-scope-network-control billyzhang2010/scope-network-control:latest
```

### Kubernetes

If you want to use the Scope Network Control plugin in an already set up Kubernetes cluster with Weave Scope running on it, you just need to run:

```
kubectl create -f https://github.com/billyzhang2010/scope-network/tree/master/deployments/k8s-network-control.yaml
```

### Recompiling an image

```
git clone git@github.com:weaveworks-plugins/scope-network-control.git
cd scope-network-control; make;
```

## Visualization

The parameters are shown in a table named **Network Control**. The plugin shows the values of latency and packet loss that are enforced on the network interface. The "-" mean that no value is set for that parameter, latency is displayed in *milliseconds* (ms) and packet loss in *percentage*.

## Controls

The Scope Network Controls plugin provides a simple interface to change the value of latency (hourglass buttons) and packet loss (scissor button) or remove value that was set (circled cross button). Such buttons are displayed on the top of the container detailed view, just above the *STATUS* section (See picture below, control are shown inside the green rectangle).

<img src="imgs/controls.png" width="512
" alt="Scope Probe plugin screenshot" align="center">

The *hourglass* buttons control the latency, from left to right they set: *2000ms*, *1000ms*, and *500ms*.
The *scissor* button controls the packet loss, it sets a 10% packet loss.
The *circled cross* button clear any previous settings.
