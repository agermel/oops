package docker

import (
	"bytes"
	"fmt"
	"net/http"

	"oops/internal/nodelet"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// ContainerExec 在指定容器内执行命令，返回 stdout、stderr 和退出码。
// 非 TTY，非交互——单次命令执行，输出通过 stdcopy.StdCopy 分离。
func (c *Client) ContainerExec(r *http.Request, containerID string, cmd []string) (nodelet.ExecResult, error) {
	if err := validateExecCommand(cmd); err != nil {
		return nodelet.ExecResult{}, fmt.Errorf("sandbox: %w", err)
	}

	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nodelet.ExecResult{}, err
	}

	// 1. 创建 exec 实例。
	createResp, err := c.api.ExecCreate(r.Context(), containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  false,
		TTY:          false,
	})
	if err != nil {
		return nodelet.ExecResult{}, fmt.Errorf("exec create: %w", err)
	}

	// 2. 附加到 exec 实例，收集输出。
	attachResp, err := c.api.ExecAttach(r.Context(), createResp.ID, client.ExecAttachOptions{
		TTY: false,
	})
	if err != nil {
		return nodelet.ExecResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, err = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attachResp.Reader)
	if err != nil {
		return nodelet.ExecResult{}, fmt.Errorf("exec read: %w", err)
	}

	// 3. 查询退出码。
	inspectResp, err := c.api.ExecInspect(r.Context(), createResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return nodelet.ExecResult{}, fmt.Errorf("exec inspect: %w", err)
	}

	return nodelet.ExecResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: inspectResp.ExitCode,
	}, nil
}
