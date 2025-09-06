{{if .EnableCron}}package job

import (
	"context"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"github.com/rushteam/beauty/pkg/service/cron"
)

// CronJobs 定时任务管理器
type CronJobs struct {
	cfg *config.Config
}

// NewCronJobs 创建定时任务管理器
func NewCronJobs(cfg *config.Config) *CronJobs {
	return &CronJobs{cfg: cfg}
}

// GetOptions 获取定时任务选项
func (c *CronJobs) GetOptions() []cron.CronOptions {
	return []cron.CronOptions{
		// 每分钟执行一次的任务
		cron.WithCronHandler("@every 1m", c.healthCheck),
		
		// 每小时执行一次的任务
		cron.WithCronHandler("@hourly", c.cleanup),
		
		// 每天凌晨2点执行的任务
		cron.WithCronHandler("0 2 * * *", c.dailyReport),
		
		// 每周一凌晨3点执行的任务
		cron.WithCronHandler("0 3 * * 1", c.weeklyReport),
	}
}

// healthCheck 健康检查任务
func (c *CronJobs) healthCheck(ctx context.Context) error {
	slog.Info("执行健康检查任务")
	
	// 在这里实现健康检查逻辑
	// 例如：检查数据库连接、检查外部服务等
	
	return nil
}

// cleanup 清理任务
func (c *CronJobs) cleanup(ctx context.Context) error {
	slog.Info("执行清理任务")
	
	// 在这里实现清理逻辑
	// 例如：清理过期数据、清理临时文件等
	
	return nil
}

// dailyReport 日报任务
func (c *CronJobs) dailyReport(ctx context.Context) error {
	slog.Info("执行日报任务")
	
	// 在这里实现日报逻辑
	// 例如：生成每日统计报告、发送邮件等
	
	return nil
}

// weeklyReport 周报任务
func (c *CronJobs) weeklyReport(ctx context.Context) error {
	slog.Info("执行周报任务")
	
	// 在这里实现周报逻辑
	// 例如：生成每周统计报告、数据备份等
	
	return nil
}
{{end}}
