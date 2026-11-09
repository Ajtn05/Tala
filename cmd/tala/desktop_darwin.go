package main

import (
	"tala/internal/app"
	"tala/internal/dashboard"
)

func launchDesktop(output string) error {
	dashboard.Run(func() {
		setupLogging(output)
		controller, err := app.New(output)
		if err != nil {
			dashboard.StartupError(err.Error())
			return
		}
		host := dashboard.New(controller)
		host.OnQuit = func() {
			controller.Close()
			dashboard.Stop()
		}
		host.OnEvent = func(event app.Event) {
			switch event.Type {
			case "live", "settings":
				host.SyncMenuBar()
			}
		}
		host.Init()
	})
	return nil
}

func notifyExisting() { dashboard.NotifyExisting() }
