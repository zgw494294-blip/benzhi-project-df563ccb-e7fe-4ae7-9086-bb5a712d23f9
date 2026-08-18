# BENZHI_README

## 项目说明
- 项目：benzhi-project-df563ccb-e7fe-4ae7-9086-bb5a712d23f9
- 项目用途：StallSettle 已扩展为完整可用的中文寄售工作台，支持草稿基本信息与商品维护、状态和关键词筛选、库存及销售统计、结算预览、版本化写入、幂等销售和原子本地账本。构建、完整流程自检、全部测试、静态检查及桌面和移动端浏览器验证均已通过。
- Go 工具链：`golang:1.22.0`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/stallsettle -smoke
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-df563ccb-e7fe-4ae7-9086-bb5a712d23f9-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-df563ccb-e7fe-4ae7-9086-bb5a712d23f9-arm64 linux/arm64
docker run -it benzhi-project-df563ccb-e7fe-4ae7-9086-bb5a712d23f9-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/stallsettle -smoke`
