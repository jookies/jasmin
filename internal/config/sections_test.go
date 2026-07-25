package config

import "testing"

func TestLoadAMQPDefaultsAndValues(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = broker-a\nusername = app\npassword = secret\nvhost = /prod\nport = 5673\nheartbeat = 30\nconnection_loss_retry = no\n")
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Host != "broker-a" || amqp.Username != "app" || amqp.Password != "secret" || amqp.Vhost != "/prod" {
		t.Fatalf("amqp fields = %+v", amqp)
	}
	if amqp.Port != 5673 || amqp.Heartbeat != 30 {
		t.Fatalf("amqp ints = %+v", amqp)
	}
	if amqp.ReconnectOnLoss || !amqp.ReconnectOnFailure {
		t.Fatalf("reconnect flags = %+v", amqp)
	}
	if amqp.ReconnectLossDelay != 10 || amqp.ReconnectFailureDelay != 10 {
		t.Fatalf("reconnect delays = %+v", amqp)
	}
}

func TestLoadAMQPDefaultsWhenAbsent(t *testing.T) {
	file := mustParse(t, "[other]\nx = 1\n")
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Host != "127.0.0.1" || amqp.Username != "guest" || amqp.Password != "guest" ||
		amqp.Vhost != "/" || amqp.Port != 5672 {
		t.Fatalf("amqp defaults = %+v", amqp)
	}
}

func TestAMQPURLBuilding(t *testing.T) {
	base := AMQP{Host: "h", Username: "u", Password: "p", Port: 5672, Vhost: "/"}
	if got := base.URL(); got != "amqp://u:p@h:5672/" {
		t.Fatalf("default vhost url = %q", got)
	}
	named := AMQP{Host: "h", Username: "u", Password: "p", Port: 5672, Vhost: "/prod"}
	if got := named.URL(); got != "amqp://u:p@h:5672/prod" {
		t.Fatalf("named vhost url = %q", got)
	}
}

func TestLoadAMQPCloudURLOverride(t *testing.T) {
	file := mustParse(t, "[amqp-broker]\nhost = ignored\n")
	file.getenv = withEnv(map[string]string{"CLOUDAMQP_URL": "amqps://user:pa_ss-1@rabbit.example.com/vh"})
	amqp, err := LoadAMQP(file)
	if err != nil {
		t.Fatal(err)
	}
	if amqp.Username != "user" || amqp.Password != "pa_ss-1" || amqp.Host != "rabbit.example.com" || amqp.Vhost != "vh" {
		t.Fatalf("cloud override = %+v", amqp)
	}
	// A malformed CLOUDAMQP_URL errors.
	file.getenv = withEnv(map[string]string{"CLOUDAMQP_URL": "not-a-url"})
	if _, err := LoadAMQP(file); err == nil {
		t.Fatal("malformed CLOUDAMQP_URL must error")
	}
}

func TestLoadRedisDefaultsAndValues(t *testing.T) {
	file := mustParse(t, "[redis-client]\nhost = redis-a\nport = 6380\npassword = pw\ndbid = 3\npoolsize = 20\n")
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Host != "redis-a" || redis.Port != 6380 || redis.Password != "pw" || redis.DBID != 3 || redis.PoolSize != 20 {
		t.Fatalf("redis fields = %+v", redis)
	}
}

func TestLoadRedisDefaults(t *testing.T) {
	file := mustParse(t, "[x]\ny=1\n")
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Host != "127.0.0.1" || redis.Port != 6379 || redis.Password != "" || redis.DBID != 0 || redis.PoolSize != 10 {
		t.Fatalf("redis defaults = %+v", redis)
	}
}

func TestRedisURLBuilding(t *testing.T) {
	withPass := Redis{Host: "h", Port: 6379, Password: "pw", DBID: 2}
	if got := withPass.URL(); got != "redis://:pw@h:6379/2" {
		t.Fatalf("redis url with pass = %q", got)
	}
	noPass := Redis{Host: "h", Port: 6379, DBID: 0}
	if got := noPass.URL(); got != "redis://h:6379/0" {
		t.Fatalf("redis url no pass = %q", got)
	}
}

func TestLoadRedisURLOverride(t *testing.T) {
	file := mustParse(t, "[redis-client]\nhost = ignored\n")
	file.getenv = withEnv(map[string]string{"REDIS_URL": "redis://:pass123@cache.example.com:6381"})
	redis, err := LoadRedis(file)
	if err != nil {
		t.Fatal(err)
	}
	if redis.Password != "pass123" || redis.Host != "cache.example.com" || redis.Port != 6381 {
		t.Fatalf("redis url override = %+v", redis)
	}
}
