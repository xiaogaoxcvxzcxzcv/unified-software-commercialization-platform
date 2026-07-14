# Deploy

此目录保存 Docker Compose、环境变量模板、备份与恢复编排。数据库迁移的唯一权威目录是 `../backend/migrations/`，deploy 只能调用受控迁移工具，不得再保存第二套迁移文件。真实密钥和生产数据不得进入仓库。
