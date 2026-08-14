// Throwaway: runs AutoMigrate against a scratch database so the resulting
// schema can be dumped as the first real SQL migration.
package main

import (
	"context"
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"service_nusantara/internal/model"
)

func main() {
	db, err := gorm.Open(postgres.Open(os.Args[1]), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	if err := model.AutoMigrate(context.Background(), db); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	fmt.Println("schema created:", len(model.All()), "models")
}
