-- 本文件负责把 Boss 直聘 position 配置中仍为占位符的关键选择器修复为真实值。
-- 选择器来源：后端目录 boss.json（老版 Chrome 扩展架构实测值，入口页同为 /web/chat/recommend）。
-- 背景：0036 写入占位符后，0039 只修复了 itemText 和 clickTarget，
-- current/switchBtn/list/item 四个关键选择器一直是占位符，导致岗位运行读取当前岗位必然失败。
-- 带条件更新：只替换占位符文本，不覆盖管理后台已手工修改过的配置，重复执行幂等。

-- 当前岗位名称：聊天推荐页顶部的岗位下拉组件文字
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{position,current}', '{"parent_classes":[[".ui-dropmenu.ui-dropmenu-label-arrow.ui-dropmenu-drop-arrow.job-selecter-wrap"]],"target_classes":[[".ui-dropmenu-label"]]}'::jsonb, true)
WHERE config_key = 'platform.boss'
  AND enabled = true
  AND config_value #> '{position,current}' IS NOT NULL
  AND config_value #>> '{position,current,target_classes,0,0}' LIKE '%请改成真实CSS%';

-- 岗位切换按钮：与当前岗位同一个下拉组件
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{position,switchBtn}', '{"parent_classes":[[".ui-dropmenu.ui-dropmenu-label-arrow.ui-dropmenu-drop-arrow.job-selecter-wrap"]],"target_classes":[[".ui-dropmenu-label"]]}'::jsonb, true)
WHERE config_key = 'platform.boss'
  AND enabled = true
  AND config_value #> '{position,switchBtn}' IS NOT NULL
  AND config_value #>> '{position,switchBtn,target_classes,0,0}' LIKE '%请改成真实CSS%';

-- 岗位列表容器：下拉展开后的列表外层
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{position,list}', '{"parent_classes":[[".job-selecter-options"]],"target_classes":[[".job-list"]]}'::jsonb, true)
WHERE config_key = 'platform.boss'
  AND enabled = true
  AND config_value #> '{position,list}' IS NOT NULL
  AND config_value #>> '{position,list,target_classes,0,0}' LIKE '%请改成真实CSS%';

-- 岗位列表项：列表内的单个岗位
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{position,item}', '{"parent_classes":[[".job-list"]],"target_classes":[[".label"]]}'::jsonb, true)
WHERE config_key = 'platform.boss'
  AND enabled = true
  AND config_value #> '{position,item}' IS NOT NULL
  AND config_value #>> '{position,item,target_classes,0,0}' LIKE '%请改成真实CSS%';

-- 岗位名称文本：0039 误将 .label 修为无点号的 label（会被当成 HTML 标签名），补上点号
UPDATE system_configs
SET config_value = jsonb_set(config_value, '{position,itemText}', '{"target_classes":[[".label"]]}'::jsonb, true)
WHERE config_key = 'platform.boss'
  AND enabled = true
  AND config_value #> '{position,itemText}' IS NOT NULL
  AND config_value #>> '{position,itemText,target_classes,0,0}' = 'label';
