package docker

import (
	"fmt"
	"strings"
)

// blockedCommands 是禁止在容器内执行的破坏性命令。
var blockedCommands = map[string]string{
	// 文件破坏
	"rm":     "禁止删除文件",
	"rmdir":  "禁止删除目录",
	"unlink": "禁止删除文件",
	"shred":  "禁止粉碎文件",

	// 磁盘/文件系统破坏
	"dd":      "禁止直接操作块设备",
	"mkfs":    "禁止创建文件系统",
	"fdisk":   "禁止操作分区表",
	"parted":  "禁止操作分区表",
	"mkswap":  "禁止创建交换分区",
	"mount":   "禁止挂载操作",
	"umount":  "禁止卸载操作",

	// 系统控制
	"reboot":   "禁止重启",
	"shutdown": "禁止关机",
	"halt":     "禁止关机",
	"poweroff": "禁止关机",
	"init":     "禁止切换运行级别",

	// 进程终止
	"kill":    "禁止终止进程",
	"killall": "禁止终止进程",
	"pkill":   "禁止终止进程",

	// 权限更改
	"chmod": "禁止修改文件权限",
	"chown": "禁止修改文件所有者",
	"chgrp": "禁止修改文件所属组",

	// 权限提升
	"sudo": "禁止提权",
	"su":   "禁止切换用户",

	// 网络下载
	"wget": "禁止下载文件",
	"curl": "禁止下载文件",
	"nc":   "禁止网络连接",

	// 包管理
	"apt":     "禁止安装软件包",
	"apt-get": "禁止安装软件包",
	"yum":     "禁止安装软件包",
	"dnf":     "禁止安装软件包",
	"apk":     "禁止安装软件包",
	"pip":     "禁止安装 Python 包",
	"pip3":    "禁止安装 Python 包",
	"npm":     "禁止安装 Node.js 包",
}

// blockedPatterns 是参数中的危险模式。匹配任一模式即拒绝。
var blockedPatterns = []struct {
	pattern string
	desc    string
}{
	{">", "禁止输出重定向（写入文件）"},
	{">>", "禁止追加重定向"},
	{"`", "禁止命令替换"},
	{"$(", "禁止命令替换"},
	{";", "禁止命令链"},
	{"&&", "禁止命令链"},
	{"||", "禁止命令链"},
	{"|", "禁止管道"},
	{"/dev/", "禁止访问设备文件"},
	{">/", "禁止写入系统路径"},
}

// validateExecCommand 校验 exec 命令是否安全。如果命令不安全，返回错误描述。
func validateExecCommand(cmd []string) error {
	if len(cmd) == 0 {
		return fmt.Errorf("命令为空")
	}

	// 1. 基础命令黑名单。
	base := cmd[0]
	if reason, blocked := blockedCommands[base]; blocked {
		return fmt.Errorf("%s: %s", reason, base)
	}

	// 2. 参数危险模式检测。
	for _, arg := range cmd {
		for _, bp := range blockedPatterns {
			if strings.Contains(arg, bp.pattern) {
				return fmt.Errorf("%s（参数含 %q）", bp.desc, bp.pattern)
			}
		}
	}

	// 3. 整体命令字符串检测（覆盖拼接场景）。
	full := strings.Join(cmd, " ")
	for _, bp := range blockedPatterns {
		if strings.Contains(full, bp.pattern) {
			return fmt.Errorf("%s（命令含 %q）", bp.desc, bp.pattern)
		}
	}

	return nil
}
