# Commerce Process 模块

Commerce Process 负责购买、支付确认、权益授予、退款和权益回收的跨模块流程进度。

## 拥有的数据

- commerce_processes
- commerce_process_steps
- commerce_process_failures

## 不负责

- 不计算商品价格。
- 不验证支付 Provider 回调。
- 不直接写订单、支付或权益表。

