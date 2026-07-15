# Assembly 迁移与参考说明

现有演示后台中的“创建产品”和“能力开关”只作为交互参考，不能直接视为装配器。

迁移时保留 Product、Application、Tenant 和能力配置事实；新增蓝图、装配计划、Manifest 和锁定清单，不把它们塞进 Product JSON。旧接入软件先建立基线蓝图和 lock，再进入后续升级管理。

禁止从参考项目复制代码生成器或把 PowerShell/字符串替换脚本作为长期生成协议。生成必须基于结构化 Manifest、受控模板、文件所有权和可重复测试。
