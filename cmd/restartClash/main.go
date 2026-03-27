package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	// 替换为你的 Clash for Windows 路径
	clashPath := `D:\software\commonTool\Clash.for.Windows-0.19.24-win\Clash for Windows.exe`

	fmt.Println("正在关闭 Clash for Windows...")

	// 结束进程
	cmd := exec.Command("taskkill", "/F", "/IM", "Clash for Windows.exe")
	cmd.Run()

	// 等待 2 秒
	time.Sleep(2 * time.Second)

	fmt.Println("正在启动 Clash for Windows...")

	// 启动进程
	err := exec.Command("cmd", "/c", "start", "", clashPath).Run()
	if err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("重启完成！")
}
