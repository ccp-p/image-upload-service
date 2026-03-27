package main

import (
	"fmt"
	"time"

	"github.com/go-vgo/robotgo"
)

func main() {
	fmt.Println("脚本启动，请在 3 秒内将鼠标移动到要撤回的消息上...")
	time.Sleep(3 * time.Second)

	// 1. 模拟鼠标右键点击当前位置
	robotgo.Click("right", false)
	
	// 等待右键菜单弹出
	time.Sleep(200 * time.Millisecond)

	// 2. 模拟按下 'r' 键。在 Windows 微信版中，右键菜单中的“撤回(R)”对应快捷键 R
	robotgo.KeyTap("r")

	fmt.Println("操作完成")
}
