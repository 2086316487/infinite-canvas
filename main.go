package main

import (
	"log"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/router"
	"github.com/basketikun/infinite-canvas/service"
)

func main() {
	if err := config.Load(); err != nil {
		log.Fatal(err)
	}

	if config.WebEnabled() {
		if err := service.EnsureDefaultAdmin(); err != nil {
			log.Fatal(err)
		}
	}

	if config.ImageTaskWorkerEnabled() {
		if config.WebEnabled() {
			if err := service.StartImageTaskScheduler(); err != nil {
				log.Fatal(err)
			}
		} else {
			log.Printf("start image task worker mode role=%s", config.Cfg.AppRole)
			if err := service.RunImageTaskScheduler(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}

	if !config.WebEnabled() {
		log.Printf("app role=%s, web server disabled", config.Cfg.AppRole)
		return
	}

	service.StartPromptSyncScheduler()
	log.Fatal(router.New().Run(":" + config.Cfg.Port))
}
