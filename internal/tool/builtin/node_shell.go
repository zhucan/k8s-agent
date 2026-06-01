package builtin

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/rest"

	"github.com/k8s-inspect/internal/nodes"
)

// NodeShellExecutor executes commands on a node by creating a privileged Pod.
type NodeShellExecutor struct {
	CS         *kubernetes.Clientset
	RestConfig *rest.Config
	Nodes      *nodes.Registry
}

// execOnNodeViaPod runs a shell command on the specified node via a short-lived privileged Pod.
func (e *NodeShellExecutor) execOnNodeViaPod(ctx context.Context, nodeName, command string) (string, error) {
	namespace := "default"
	podName := fmt.Sprintf("node-shell-%d", time.Now().Unix())

	hostPathType := corev1.HostPathDirectory
	privileged := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "node-shell",
			},
		},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			HostNetwork:   true,
			HostPID:       true,
			HostIPC:       true,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "shell",
					Image:   "swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/busybox:1.36",
					Command: []string{"/bin/sh", "-c", "sleep 3600"},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host-root",
							MountPath: "/host",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "host-root",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/",
							Type: &hostPathType,
						},
					},
				},
			},
		},
	}

	_, err := e.CS.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create pod: %w", err)
	}

	defer func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.CS.CoreV1().Pods(namespace).Delete(deleteCtx, podName, metav1.DeleteOptions{})
	}()

	if err := e.waitForPodRunning(ctx, namespace, podName); err != nil {
		return "", fmt.Errorf("wait pod running: %w", err)
	}

	// Execute the command inside the pod using chroot to access the host filesystem
	execCommand := []string{"chroot", "/host", "/bin/sh", "-c", command}
	output, err := e.execInPod(ctx, namespace, podName, "shell", execCommand)
	if err != nil {
		return "", fmt.Errorf("exec in pod: %w", err)
	}

	return output, nil
}

// waitForPodRunning polls until the pod enters Running state or the deadline is exceeded.
func (e *NodeShellExecutor) waitForPodRunning(ctx context.Context, namespace, podName string) error {
	timeout := 120 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		pod, err := e.CS.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if pod.Status.Phase == corev1.PodRunning {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name == "shell" && cs.Ready {
					return nil
				}
			}
		}

		if pod.Status.Phase == corev1.PodFailed {
			return fmt.Errorf("pod failed: %s", pod.Status.Message)
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				if cs.State.Waiting.Reason == "ImagePullBackOff" || cs.State.Waiting.Reason == "ErrImagePull" {
					return fmt.Errorf("image pull failed: %s (image: %s)", cs.State.Waiting.Message, pod.Spec.Containers[0].Image)
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	pod, err := e.CS.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("timeout waiting for pod to be running (current phase: %s, message: %s)", pod.Status.Phase, pod.Status.Message)
	}

	return fmt.Errorf("timeout waiting for pod to be running")
}

// execInPod executes a command inside a running pod container and returns stdout+stderr.
func (e *NodeShellExecutor) execInPod(ctx context.Context, namespace, podName, containerName string, command []string) (string, error) {
	req := e.CS.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.RestConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("create executor: %w", err)
	}

	stdout := &writeBuffer{}
	stderr := &writeBuffer{}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("stream: %w", err)
	}

	return output, nil
}

// writeBuffer implements io.Writer by accumulating bytes in a slice.
type writeBuffer struct {
	data []byte
}

func (w *writeBuffer) Write(p []byte) (n int, err error) {
	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *writeBuffer) String() string {
	return string(w.data)
}

func (w *writeBuffer) Len() int {
	return len(w.data)
}

var _ io.Writer = (*writeBuffer)(nil)
