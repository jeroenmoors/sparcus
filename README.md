# Sparcus
## What is Sparcus?
Sparcus is an HTTP-driven key-value store and script launcher. It is designed to execute scripts based on events, primarily in a home automation environment.

### Key value store
To set a value `temperature/garden` make a HTTP-request to:
```
http://ip-of-sparcus/set/temperature/garden?value=29
```
To retrieve this value, call:
```
http://ip-of-sparcus/get/temperature/garden
```
To retrieve the average of the last 6 values call:
```
http://ip-of-sparcus/get/temperature/garden?average=6
```
To receive the response as json:
```
http://ip-of-sparcus/get/temperature/garden?format=json
```
Available format are `plain`,`json`,`csv`, `pipe`

The keystore is persistent as upon shutdown the current state will be written to `data.json`.

### Script launcher
To execute scripts, Sparcus needs a `handler` diretory (default `/var/lib/sparcus/handlers`). Under this directory a similar structure as used for the key-value store can be created. When a set HTTP-request is received, all scripts in the corrospondig directory and directories below will be executed.

For example the set request above will trigger the two first scripts, but not the third:
```
/always.sh
/temperature/garden/shades.sh
/temperature/pool/pump.sh
```
When a script is executed, it can get some usefull info from these environment variables:
```
EVENT_PATH         => URI of the request
EVENT_PATH_DOTTED  => URI with dots instead of slashes
EVENT_VALUE        => Value provided by ?value=
EVENT_VALUE_AVG_3  => Average of the last 3 values
EVENT_VALUE_AVG_5  => Average of the last 5 values
EVENT_VALUE_AVG_10 => Average of the last 10 values
```
Other values can be retrieved using the `/get/` HTTP-request with your favorite HTTP-requesting method, `wget`, `curl`, `GET`, `file_get_contents()`,..

### Security
**Sparcus by itself is not secure!** 
Only use it on a trusted local network or even better add a webserver providing authentication in front of it.

In future versions authentication might be build in.


## Installation
Create a config `/etc/sparcus.conf`:
```
Port 80
User sparcus
Group sparcus
HandlersPath /var/lib/sparcus/handlers
DataFile /var/lib/sparcus/data.json
MaxEvents 250
GraphiteHost localhost
GraphitePort 2003
```
Run `sparcus`
