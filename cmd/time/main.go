package main

import (
	"fmt"
	"time"

	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

const (
	WorkDuration = 25 * time.Minute
	BreakDuration = 5 * time.Minute
)

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("番茄钟可视化")

	timerLabel := widget.NewLabelWithStyle("25:00", fyne.TextAlignCenter, fyne.TextStyle{Bold: true, Monospace: true})
	statusLabel := widget.NewLabelWithStyle("准备开始", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	infoLabel := widget.NewLabel("正在计算时间...") // 确保在此定义

	// 新增：进度条和刻度
	progress := widget.NewProgressBar()
	progress.Max = 1.0

	// 修正：删除不支持的 SetOnTop，改用驱动层面的提示或通过调整窗口属性（Fyne 核心接口暂不统一支持 SetOnTop）
	// 为了保持功能，我们仍然保留 UI 开关作为占位，或者暂时移除以修复编译
	onTopCheck := widget.NewCheck("始终置顶", func(on bool) {
		// 注意：标准 fyne.Window 接口目前并不直接暴露 SetOnTop
		// 在某些桌面环境下无法直接实现。此处先注释掉功能以修复编译
		// fmt.Println("置顶功能:", on) 
	})

	// 5分钟一个刻度的容器
	ticks := container.NewHBox(layout.NewSpacer())
	for i := 0; i < 5; i++ {
		dot := canvas.NewCircle(color.Gray{Y: 0xaa})
		dot.Resize(fyne.NewSize(10, 10)) // 修正方法名
		ticks.Add(container.NewGridWrap(fyne.NewSize(10, 10), dot)) // 使用容器固定大小
		ticks.Add(layout.NewSpacer())
	}

	updateInfo := func() {
		now := time.Now()
		var target string
		var diff time.Duration
		if now.Hour() < 12 {
			lunch := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
			diff = lunch.Sub(now)
			target = "午餐"
		} else {
			offDuty := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location())
			diff = offDuty.Sub(now)
			target = "下班"
		}
		poms := float64(diff.Minutes()) / 30.0
		infoLabel.SetText(fmt.Sprintf("🕒 距离%s还有: %d 分钟 (约 %.1f 个番茄钟)", target, int(diff.Minutes()), poms))
	}

	var timerRunning bool
	startBtn := widget.NewButton("开始计时", func() {
		if timerRunning {
			return
		}
		go func() {
			timerRunning = true
			statusLabel.SetText("🚀 专注中 (25分钟)")
			// 增加一个简单的背景色或逻辑反馈
			runTimerWithProgress(WorkDuration, timerLabel, progress)
			statusLabel.SetText("☕ 休息时间 (5分钟)")
			runTimerWithProgress(BreakDuration, timerLabel, progress)
			statusLabel.SetText("✅ 阶段结束")
			timerRunning = false
		}()
	})

	myWindow.SetContent(container.NewVBox(
		container.NewHBox(layout.NewSpacer(), onTopCheck), // 置顶放在右上角
		timerLabel,
		statusLabel,
		container.NewPadded(progress),
		ticks,
		container.NewPadded(startBtn),
		widget.NewSeparator(),
		container.NewCenter(infoLabel),
	))

	// 先初始化一次，避免 infoLabel 初始为空
	updateInfo()

	myWindow.Resize(fyne.NewSize(350, 250))

	// 统一后台逻辑：仅启动一个后台协程负责所有周期性更新
	go func() {
		for {
			updateInfo()
			time.Sleep(time.Minute)
		}
	}()

	myWindow.ShowAndRun()
}

func runTimerWithProgress(duration time.Duration, label *widget.Label, progress *widget.ProgressBar) {
	end := time.Now().Add(duration)
	total := duration.Seconds()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		if now.After(end) {
			break
		}
		timeLeft := end.Sub(now)
		label.SetText(fmt.Sprintf("%02d:%02d", int(timeLeft.Minutes()), int(timeLeft.Seconds())%60))

		// 更新进度条 (倒计时增加比例)
		percent := 1.0 - (timeLeft.Seconds() / total)
		progress.SetValue(percent)
	}
	progress.SetValue(1.0)
	label.SetText("00:00")
}
