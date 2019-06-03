package offline

import "github.com/gomodule/redigo/redis"

var (
	/*
		- 根据消息发出时间计算出其过期时间 `expireat = ts+expire`
		- `ZADD msg/u:uid1/p:platform1/oms NX ts seq1`
		- 1. 如果返回0，说明已经记录过了
		- 2. 如果返回1，执行`INCR msg/om:seq1/n`
		- -  1. 如果返回1，说明是刚刚创建的，所以要执行`EXPIREAT msg/om:seq1/n expireat`和`SET msg/om:seq1/m "xxxx" NX`以及`EXPIREAT msg/om:seq1/m expireat`
		- -  2. 根据当前时间计算出未过期消息时间戳 `expirets = now-expire`
		- -  3. 直接执行`EXPIRE msg/u:uid1/p:platform1/oms expire`，因为如果过了这个时间没有更新 EXPIRE 的话，肯定消息全特么都过期了，防止用户一直没操作而产生的过多的脏数据
	*/

	/*
		KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录) msg/om:seq1/m(消息内容) msg/om:seq1/n(消息引用计数)
		ARGV : seq1(消息标识) xxxx(消息内容) ts(消息发出时间) expire(多久过期) expireat(到期时间)
	*/
	writeLua = redis.NewScript(3, `
			-- ZADD msg/u:uid1/p:platform1/oms NX
			if redis.call("ZADD", KEYS[1], "NX", ARGV[3], ARGV[1]) == 0 then
				return nil
			end
			
			-- INCR msg/om:seq1/n
			if redis.call("INCR", KEYS[3]) == 1 then
				-- EXPIREAT msg/om:seq1/n expireat
				redis.call("EXPIREAT", KEYS[3], ARGV[5])
			
				-- SET msg/om:seq1/m xxxx NX and EXPIREAT msg/om:seq1/m expireat
				redis.call("SET", KEYS[2], ARGV[2], "NX")
				redis.call("EXPIREAT", KEYS[2], ARGV[5])
			end
			
			-- EXPIRE msg/u:uid1/p:platform1/oms expire
			redis.call("EXPIRE", KEYS[1], ARGV[4])
			
			return nil
		`)

	/*
		- 若投递给客户端成功，则执行删除操作，下面三步需要丢到lua脚本里保证原子性，然后外部循环操作
		- - 1. `ZREM msg/u:uid1/p:platform1/oms seq1`
		- - 2. 上一步若返回1，则执行`DECR msg/om:seq1/n`
		- - 3. 上一步若返回<=0，则执行`DEL msg/om:seq1/m msg/om:seq1/n`)
	*/

	/*
		KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录) msg/om:seq1/m(消息内容) msg/om:seq1/n(消息引用计数)
		ARGV : seq1(消息标识)
	*/
	deleteLua = redis.NewScript(3, `
			-- ZREM msg/u:uid1/p:platform1/oms seq1
			if redis.call("ZREM", KEYS[1], ARGV[1]) ~= 1 then
				return nil
			end

			-- DECR msg/om:seq1/n
			if redis.call("DECR", KEYS[3]) > 0 then
				return nil
			end

			-- DEL msg/om:seq1/m msg/om:seq1/n
			redis.call("DEL", KEYS[2], KEYS[3])

			return nil
		`)

	/*
		- 一般在写入一批离线消息成功之后就要执行
		- 执行一发`ZREMRANGEBYSCORE msg/u:uid1/p:platform1/oms -inf (expirets` 删除过期元素，减少下面的开支
		- 而因为最大离线映射数做的清理，就需要修正引用计数了
		- 查出来对应rank的数据列表 `ZRANGE msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count`
		- 执行 `ZREMRANGEBYRANK msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count`
		- 对列表中的数据挨个执行:
		- - `DECR msg/om:seq1/n`
		- - 上一步若返回<=0，则执行`DEL msg/om:seq1/m msg/om:seq1/n`)
	*/

	/*
		KEYS : msg/u:uid1/p:platform1/oms(某用户离线映射记录)
		ARGV : expirets(已过期时间戳) max_offline_msg_count(最大离线映射数目)
	*/
	cleanLua = redis.NewScript(1, `
			-- ZREMRANGEBYSCORE msg/u:uid1/p:platform1/oms -inf (expirets
			redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", "("..ARGV[1])
			
			local max_count = tonumber(ARGV[2])
			if max_count>=0 then
				local right = tostring(max_count+1)
			
				-- ZRANGE msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count
				local seqs = redis.call("ZRANGE", KEYS[1], "0", "-"..right)
			
				if table.getn(seqs) <=0 then 
					return nil
				end
			
				-- ZREMRANGEBYRANK msg/u:uid1/p:platform1/oms 0 -max_offline_msg_count
				redis.call("ZREMRANGEBYRANK", KEYS[1], "0", "-"..right)
			
				for i, seq in ipairs(seqs) do
					local nKey = "msg/om:"..seq.."/n"
			
					-- DECR msg/om:seq1/n
					if redis.call("DECR", nKey) <= 0 then
						local mKey = "msg/om:"..seq.."/m"
					
						-- DEL msg/om:seq1/m msg/om:seq1/n
						redis.call("DEL", mKey, nKey)
					end
				end 
			end
			
			return nil
		`)
)
