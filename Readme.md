kafka dev env:   
http://wurstmeister.github.io/kafka-docker/   
start kafka: `docker-compose -f docker-compose-single-broker.yml up -d`   
close kafka: `docker-compose down -v`

# msg
项目还未完成，代码量很少，有兴趣的可以看看。

TODO或者备忘:
- kafka换到nats-streaming
- redis的存储结构要设计成支持集群的
- horn服务的雏形 (推送服务，在存储离线的同时要根据platform情况触发)
- boat net listener没设置limit，这个要以后做下压力测试才能知道怎么设置合适
- 连接的tls
- 需要测试boat能单机连接多少，若10W，那就需要去测试10W的用户瞬间全部对station执行disconnect，redis是否撑得住，应该需要更好的法子。
- 存在离线存储的任务比拉取离线任务晚执行的可能性，如果有特别强的要求，可以考虑单uid+platform下的分布式锁保证顺序同步，但是遇见处理失败重试的场景下也没辙。
- 倘若boat服务没挂，但是和etcd有临时的断开，在断开时间里，消息会丢失，解决法子就是发现断开，立即关闭自身，有必要么？
  
------------------------------------------- 以下很乱，不一定对 ----------------------------
# 整理思维
- station先检查目的用户是否存在，如果不存在则直接向 存储离线消息 且 根据消息类型决定 是否向消息队列投递通知任务
- 如果发现用户存在，则向消息队列投递下发任务，但是此下发任务的粒度应该以session还是uid呢
- 如果是uid，好处是没那么占消息队列的吞吐能力，但是如果一个uid的多session有一个失败，之后所有session的处理都可能会重复进行
- 如果是session，好处是不会存在上述的重复情况，坏处就是占消息队列的吞吐能力
- 但是相对于大部分情况来说，消息下发失败的几率很小，所以使用uid方案为优
- carrier服务在下发消息时候若发现用户没有ack应当对其执行kickout操作
- carrier服务在下发消息时候若发现用户不在线或者被kickout，应当告知station重新处理此消息(极端情况下会发生此情况，保险起见罢了)，然后对消息队列确认处理了此消息。
- 因为ack的超时机制并且carrier的消费协程数量是固定的，所以墨迹的客户端太多就会拖慢消费能力，如果能根据主机资源动态增减协程数量就好了。

- station的分发消息rpc入口调用速度加快 和 尽可能的在投递到消息队列之前来处理离线消息以增加吞吐量 两者相比，前者会更重要一点，影响用户体验。
- 所以要在carrier那边处理在线状态的判断和离线消息的处理等等

# carrier
- 保证消息到达是针对`某个uid+某个platform`来判定，如果对应项不在线，则依据对应`platform`的`离线存储`策略对消息进行存储。
- 如果`某个uid`在`同一个platform`有`多个session`，都会投递，但只要`session_id最大`的那个到达也就足够了
- 整个系统场景下，`某个uid`重复登录`同一个platform`后，其他会话是会异步收到`kickout`消息的
- `mq`收到的消息中都会带有`retry_count`，如果消息消费失败，也会`ack`，并且要将`retry_count+1`后重新投递出去
- 自己实现的`retry_count`机制是因为我们要根据不同的`retry_count`做一些事情，例如`丢弃消息`或者`离线认定`
- 自己实现的`retry_count`机制也可以很方便的只重试一个大消费任务中的部分消费任务。
- 例如在收到`to_uid`消息后，如果发现其中`N个platform`的投递或者离线存储成功，`M个`没成功，则发布`retry`给`mq`的时候就可以只带着对应的`M个`。
- 这样的好处： 因为`to_uid`消息大部分是会消费成功的，又不会像`to_uid_platform`那么的细粒度，能增加吞吐量，又能避免因`部分platform`消费失败而产生的整个`to_uid`消息的重试。
- 最后超过一定`retry_count`实在消费失败的话，就丢进`dlq`死信队列，等待报警发现，人工来处理了。

# redis结构设计(设计上暂不支持redis集群，真需要时候再考虑)

## 连接信息
```
"msg/u:uid1/ss": {
    "sid1":"platform1-bid1",
    "sid2":"platform3-bid1",
}
```
- 没有不知`uid`却要对某`session`直接进行操作的场景，所以只存如上结构即可，无需其他。
- 因为`session`的状态是在`boat`里已经保存过了的，所以就不需要写入`redis`还做一次心跳机制了。
- 由于`boat`里的`session`本来就是会随着生命周期调用`connect`和`disconnect`方法的，所以非异常情况下不会产生脏数据。
- 如果产生，也无非两种情况，皆可修正：
- 1. `boat`服务在`etcd`里已经不存在了，对应`session`被读取时修正即可。
- 2. `boat`服务还在，但是调用发消息方法时候其反馈说`session`是不存在的，修正即可。
  
### 弊端：
- 读取时候才有清除脏数据的可能，否则会永远存在。因为极端情况很少，本身数据量也不大。就不考虑了。

## 离线消息
```
// 存储消息，用两个字段，不用hash是为了一次就能批量获取消息内容
"msg/om:seq1/m": "xxx" // 消息内容
"msg/om:seq1/n": "100" // 消息引用计数，引用计数<=0时候要主动删除对应的这两条

// 某用户在某平台的离线消息 zset
// 分数皆为 timestamp ，即为 seq 的发出时间，相同 timestamp 内按 seq 字段排序
// 这样在获取时过滤过期和删除过期元素都很方便
"msg/u:uid1/p:platform1/oms": [
    "seq1",
    "seq2",
    "seq3",
    "seq4",
]
```

### 如何写入(需lua执行保证原子性)
- 根据消息发出时间计算出其过期时间 `expireat = ts+expire`
- `ZADD msg/u:uid1/p:platform1/oms NX ts seq1`
- 1. 如果返回0，说明已经记录过了
- 2. 如果返回1，执行`INCR msg/om:seq1/n`
- -  1. 如果返回1，说明是刚刚创建的，所以要执行`EXPIREAT msg/om:seq1/n expireat`和`SET msg/om:seq1/m "xxxx" NX`以及`EXPIREAT msg/om:seq1/m expireat`
- -  2. 根据当前时间计算出未过期消息时间戳 `expirets = now-expire`
- -  3. 直接执行`EXPIRE msg/u:uid1/p:platform1/oms expire`，因为如果过了这个时间没有更新 EXPIRE 的话，肯定消息全特么都过期了，防止用户一直没操作而产生的过多的脏数据

### 如何清理脏数据(需lua执行保证原子性)
- 一般在写入一批离线消息成功之后就要执行
- 执行一发`ZREMRANGEBYSCORE msg/u:uid1/p:platform1/oms -inf (expirets` 删除过期元素，减少下面的开支
- 而因为最大离线映射数做的清理，就需要修正引用计数了
- 查出来对应rank的数据列表 `ZRANGE msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count`
- 执行 `ZREMRANGEBYRANK msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count`
- 对列表中的数据挨个执行:
- - `DECR msg/om:seq1/n`
- - 上一步若返回<=0，则执行`DEL msg/om:seq1/m msg/om:seq1/n`)
    
### 如何读取(即为发送离线消息)
- 根据当前时间算出未过期消息时间戳 `expirets = now-expire`
- 下面的往复执行，直到拿不到消息为止，拿不到时请执行一发`ZREMRANGEBYSCORE msg/u:uid1/p:platform1/oms -inf (expirets`删除过期元素
- - `ZRANGEBYSCORE msg/u:uid1/p:platform1/oms expirets +inf LIMIT 0 50` 获取头部50个未过期消息
- - 执行`MGET msg/om:seq1/m msg/om:seq2/m`拿到所有消息内容
- - 投递给客户端，若失败，则重试这次消费
- - 若成功，则执行删除操作，下面三步需lua执行保证原子性，然后外部循环操作
- - 1. `ZREM msg/u:uid1/p:platform1/oms seq1`
- - 2. 上一步若返回1，则执行`DECR msg/om:seq1/n`
- - 3. 上一步若返回<=0，则执行`DEL msg/om:seq1/m msg/om:seq1/n`)

### 弊端
- 在有效期内，对于一直不拉取离线消息并且没有产生新离线消息的用户，其列表会存在一部分已经过期的消息映射。这个基本上也无法避免了。
