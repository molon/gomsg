
## write_offline.lua
KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录) msg/om:seq1/m(消息内容) msg/om:seq1/n(消息引用计数)
		ARGV : seq1(消息标识) xxxx(消息内容) ts(消息发出时间) expire(多久过期) expireat(到期时间)

redis-cli -p 9379 --ldb --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq1/m msg/om:seq1/n , seq1 xxxx 1553629052 86400 1553715452

redis-cli --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq1/m msg/om:seq1/n , seq1 xxxx 1553629052 86400 1553715452
redis-cli --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq2/m msg/om:seq2/n , seq2 xxxx 1553629052 86400 1553715452
redis-cli --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq3/m msg/om:seq3/n , seq3 xxxx 1553629052 86400 1553715452
redis-cli --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq4/m msg/om:seq4/n , seq4 xxxx 1553629052 86400 1553715452
redis-cli --eval ./script/dev/write_offline.lua msg/u:uid1/p:platform1/oms msg/om:seq5/m msg/om:seq5/n , seq5 xxxx 1553629052 86400 1553715452

## clean_offline.lua
KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录)
		ARGV : expirets(已过期时间戳) max_offline_msg_count(最大离线映射数目)

redis-cli --ldb --eval ./script/dev/clean_offline.lua msg/u:uid1/p:platform1/oms , 1553542652 2

redis-cli --eval ./script/dev/clean_offline.lua msg/u:uid1/p:platform1/oms , 1553542652 3