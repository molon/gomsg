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