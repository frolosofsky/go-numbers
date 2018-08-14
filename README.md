go-numbers
==========

Disclaimer
----------

I'm very new in Go, so if you surfed the Internet and came across this page, please don't expect to find
the best practices here.

Overview
--------

Implementation of the task given from: https://github.com/travelaudience/go-challenge

Runs HTTP service which serves 127.0.0.1:8080/numbers endpoint. The purpose of service is to
collect data from 3rd-party services, accumulate, and return to client. It also tries to response
in 500ms. The important remark of how I measure 500ms. There is a data flow:


```
                                            S  E  R  V  I  C  E
                           |-----------------------|---------|----------------|
                           | Accept TCP,           |         |                |
client -> network -> OS -> | handle HTTP header,   | Handler | Flush the data | -> OS -> network -> client
                           | find and call handler |         |                |
                           |-----------------------|---------|----------------|
                                                   ^  500ms  ^
```

So, the real maximum response time might be much longer.

Cool story
----------

The first implementation (see first commit) used channels and timers to cut fetchers which are not fit in 500ms.
It was buggy, the main problem was about http clients (here and below it's about sub-request clients, not about
clients of /numbers) cancellation: I basically had an additional per-client timeout, and there were races between
main timer and clients timeouts. So I realized I need to have single timer and to traverse all clients and cancel
them. I imagined complicated implementation (which'd have new bugs obviously) but fortunately I find a Context
pattern which perfectly serves this case and has built-in support in http package.

Limitations (TODOs)
-------------------

1. Tests with lots of concurrent requests are not very stable. All concerns and ideas are in numbers_test.go.
2. Service isn't configurable.


How to explore
--------------

I'd suggest to start code exploration from numbers_test.go/TestHTTP, it will follow you through all the main code.

How to use
----------

```
make build && ./numbers     to build and launch service
make tests                  to run tests
```

Conclusion
----------
The service ran against https://github.com/travelaudience/go-challenge/blob/master/testserver.go

```
./testserver                                        ./numbers
2018/08/14 16:25:05 /fibo: waiting 331ms.
2018/08/14 16:25:05 /primes: waiting 287ms.         2018/08/14 16:25:05 Processing took 334.863824ms

2018/08/14 16:27:18 /primes: waiting 431ms.
2018/08/14 16:27:18 /rand: waiting 268ms.           2018/08/14 16:27:19 Processing took 434.22407ms

2018/08/14 16:28:53 /rand: waiting 6ms.
2018/08/14 16:28:53 /odd: waiting 444ms.
2018/08/14 16:28:53 /fibo: waiting 11ms.
2018/08/14 16:28:53 /primes: waiting 50ms.          2018/08/14 16:28:53 Processing took 446.84615ms

2018/08/14 16:29:49 /fibo: waiting 137ms.
2018/08/14 16:29:49 /primes: waiting 331ms.
2018/08/14 16:29:49 /rand: waiting 129ms.           2018/08/14 16:29:50 Processing took 505.657352ms
2018/08/14 16:29:49 /odd: waiting 526ms.            2018/08/14 16:29:50 Get http://localhost:8090/odd: context deadline exceeded
```

The last one shows 505.657352ms. I suppose it's because:

* Go doesn't strongly guarantee that cancellation timer fires in *exactly* 500ms, rather it's saying timer fires *after* 500ms.
* After timer fired Context needs some time to propagate cancellation to sub-requests, and I need to write response.

So, if you want to be more close to 500ms you have to use a bit smaller timeout.
