-- 本迁移新增 GoodHR 5 前端公共系统配置，用于本地执行器版本校验和系统公告展示。
INSERT INTO system_configs (config_key, config_value, description, enabled)
VALUES (
  'system.app_config',
  '{
    "local_agent_version": "5.0.0",
    "email_domain_whitelist": ["qq.com", "foxmail.com", "163.com", "126.com", "yeah.net", "sina.com", "sina.cn", "sohu.com", "aliyun.com", "139.com", "189.cn", "wo.cn", "gmail.com", "outlook.com", "hotmail.com", "live.com", "icloud.com", "yahoo.com", "proton.me", "protonmail.com"],
    "announcements_enabled": true,
    "announcements": [
      {
        "id": "2026-05-26-v1",
        "title": "GoodHR 5 更新公告",
        "content": "GoodHR 5 本地执行器版本从 5.0.0 起步，低版本请及时更新。",
        "once": true,
        "enabled": true,
        "created_at": "2026-05-26"
      }
    ]
  }'::jsonb,
  '前端公共系统配置：本地执行器版本要求、邮箱域名白名单和系统公告列表',
  true
)
ON CONFLICT (config_key) DO NOTHING;
