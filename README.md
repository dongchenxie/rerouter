**A 站 (Golang) — 爬虫返回页面，真人跳转 B 站**

- 爬虫访问：A 站直接抓取并返回 B 站对应 URL 的内容（支持本地文件缓存，TTL 可配置）。
- 真人访问：A 站 302/307 跳转到 B 站对应 URL（路径、查询参数 1:1 保持）。
- 默认缓存 sitemap、blog、products 等页面；可通过环境变量或 `config.json` 配置。

快速开始

- 本地（需要 Go 1.22）：
  - 设置环境变量：`export B_BASE_URL=https://your-b-site.example.com`
  - 运行：`go run .`
  - 打开：`http://localhost:8080`

在宿主机安装 Go（无 Docker）

- Linux（推荐脚本，需 sudo）：
  - 赋权并执行：`chmod +x scripts/install-go-linux.sh && ./scripts/install-go-linux.sh`
  - 重新打开终端或 `source ~/.zshrc`/`source ~/.bashrc`
  - 验证：`go version`
  - 运行：`B_BASE_URL=https://your-b-site.example.com go run .`
- macOS：`brew install go`，然后同上运行。
- Windows：用 `winget install Go.Go` 或从 go.dev 下载 MSI 安装包。

Makefile（可选）

- 构建二进制：`make build`（输出 `dist/a-site`）
- 直接运行：`make run`（需要先设置 `B_BASE_URL` 环境变量）
- 清理：`make clean`

- 开发容器（热重载）：
  - `chmod +x scripts/dev-up.sh && ./scripts/dev-up.sh`
  - 修改代码自动重载。默认监听 `:8080`。

- 生产容器：
  - `chmod +x scripts/prod-up.sh && ./scripts/prod-up.sh`
  - 构建静态二进制，多阶段构建，最终基于 `alpine`，内置 CA 证书。

配置（环境变量优先，亦可用 `config.json`）

- `B_BASE_URL`：B 站根地址（必填），例：`https://b.example.com`
- `LISTEN_ADDR`：监听地址，默认 `:8080`
- `CACHE_DIR`：缓存目录，默认 `./cache`
- `CACHE_TTL_SECONDS`：缓存过期秒数，默认 `3600`
- `CACHE_PATTERNS`：逗号分隔的路径匹配，支持 `*`，默认 `/sitemap.xml,/blog/*,/products/*`
- `REDIRECT_STATUS`：真人跳转状态码，默认 `302`（可设为 `307`）
- `CONFIG_PATH`：可选，JSON 配置文件路径，默认 `./config.json`（示例见 `config.sample.json`）

行为说明

- 爬虫识别：基于常见 UA 关键字（Googlebot/Bingbot/Baiduspider 等）。可在请求头加 `X-Bot: true` 做联调测试。
- 缓存策略：仅对 GET/HEAD 且匹配 `CACHE_PATTERNS` 的路径缓存。缓存内容为最小头部集（Content-Type/Last-Modified/ETag）与 Body。
- 机器访问透传：不在缓存范围内的爬虫请求将直接抓取 B 站并返回（不缓存）。
- `robots.txt`：A 站内置 `Allow: /`，确保可抓取。
- 健康检查：`/healthz` 返回 `ok`。

例子（Nginx/域名）

- 将 `a.com` 指向本服务（A 站）。
- `B_BASE_URL` 设置为 `https://b.com`；人类访问 `https://a.com/x` 会跳转 `https://b.com/x`，爬虫访问 `https://a.com/x` 返回 B 站 `/x` 的实际内容。

注意

- B 站请自行配置 `robots.txt` 或 WordPress 设置，避免被搜索引擎收录（A 站负责对外展示与抓取）。
- 如需更复杂的 UA 识别、IP 白名单或预热缓存，可在本项目基础上扩展。
