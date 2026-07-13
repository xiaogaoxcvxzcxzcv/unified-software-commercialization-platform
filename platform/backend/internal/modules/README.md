# 业务模块边界

每个目录是一个业务事实的唯一所有者。实现模块时，在本模块内部使用：

```text
transport -> application -> domain -> ports <- adapters
```

跨模块只能调用对方公开的 application 接口或消费版本化领域事件。禁止导入其他模块的 adapters/Repository，禁止查询或写入其他模块的数据表。这里的目录存在不代表相应业务能力已经实现；能力完成状态以全局能力索引、契约与测试为准。
