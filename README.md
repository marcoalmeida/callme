# callme
Sometimes a task needs to be scheduled later execution at some point in the future.
`callme` can help with that.

Simply register a task providing a name, the time at which it needs to happen (with 1-minute resolution), and an HTTP 
callback endpoint. When the time comes, `callme` will make an HTTP call to the endpoint.

Optional parameters like the HTTP method to use, payload to send, expected HTTP response status code, maximum 
number of retries, and maximum acceptable delay can also be set.

### Simple usage examples
* Schedule a request to `http://example.com/that-thing/run-it` to happen 6 hours from now as a simple `GET` 
with no request body, and expecting `200` to be the server's response: 
  ```
  curl -XPUT -H "Content-Type: application/json" \
	      --data '{"trigger_at":"+6h", "callback":"http://example.com/that-thing/run-it"}' \
	      callme:6777/task/simpletask
  ```

* Retrieve the state of the scheduled task:
  ```
  curl -XGET -H "Content-Type: application/json" callme:6777/task/simpletask?pretty
  ```

### Advanced exampled
* Schedule two tasks on `http://example.com/far-far-away` for midnight and 01:00am on 2028-11-26, expecting the 
response status code to be `204`:
  ```
  curl -XPUT -H "Content-Type: application/json" \
	      --data '{"trigger_at":"1858809600", "callback":"http://example.com/far-far-away", "expected_http_status":204}' \
	      callme:6777/task/faraway
  curl -XPUT -H "Content-Type: application/json" \
	      --data '{"trigger_at":"1858816800", "callback":"http://example.com/far-far-away", "expected_http_status":204}' \
	      callme:6777/task/faraway
  ```

* Retrieve the state of both scheduled tasks:
  ```
  curl -XGET -H "Content-Type: application/json" callme:6777/task/faraway?pretty
  ```

* Retrieve the state of the task scheduled for midnight:
  ```
  curl -XGET -H "Content-Type: application/json" callme:6777/task/faraway@1858809600?pretty
  ```
  
* Retrieve the state of *all* scheduled tasks:
  ```
  curl -XGET -H "Content-Type: application/json" callme:6777/task/?pretty
  ```

### JSON payload for a task definition
| Parameter  | Type  | Required  | Default  | Description  |
|---|---|---|---|---|
| `task_name` | string  | Yes | N/A | Name of the task being scheduled. |
| `trigger_at` | string | Yes | N/A | When to run the task, i.e., call the `callback` endpoint. Must be either a Unix timestamp with 1-minute resolution or a relative time definition of the form `+<integer>{m,h,d}` where the last letter represents minutes, hours, and days respectively. |
| `callback` | string | Yes | N/A | Endpoint to request when the current minute matches `trigger_at`. |
| `callback_method` | string | No | `GET` | HTTP method to use when requesting the `callback` endpoint. |
| `payload` | string | No | "" | Payload to send with the request to the `callback` endpoint. |
| `expected_http_status` | integer | No | 200 | HTTP status code the server is expected to respond with on a successful request to `callback`. |
| `retry` | integer | No | 1 | Maximum number of times to retry failed requests to `callback` before marking the task as failed. |
| `max_delay` | integer | No | 10min | Do not make a request to `callback` if only starting it `max_delay` or more 
minutes after `trigger_at` |

### API reference
* Create a new scheduled task:

  `PUT /task/<task_name>`

  The request body is a JSON object as per the section above.
  
  
* Reschedule failed tasks:

  * A specific entry:
    `POST /reschedule/<task_name>@<trigger_at>`
  
  * All entries of a given task, identified by name:
     `POST /reschedule/<task_name>`
  
  The optional request body is a JSON object with a single key, the new time at the the task(s) should run:     
  `{"trigger_at":"<time_specifier>"}`, which exactly matches the `trigger_at` parameter descried in the section above.
  
  If `trigger_at` is not provided in the request body the scheduled time will be set to the next minute.
  
  By default only failed tasks are rescheduled. This behavior can be overridden by adding the `all=true` to the query 
  string. 

* Retrieve state

  `GET /status/<task_name>@<trigger_at>`
  
  Retrieves the state of a specific entry of a given task. Tasks are uniquely identified by their name and time at 
  which they should be triggered.
  
  `GET /status/<task_name>`
  
  Retrieves the state of all entries of a given task. The output, a JSON object, is paginated and may include `next` 
  as a key.
  In order to retrieve the next batch of results, the next call to `/status` should include the `start_from` query 
  string parameter with the name and trigger time of a key:
  
    `start_from=<task_name>@<trigger_at>`  
  
  By default all entries are returned. It's possible to filter out past ones by adding `future_only` as a query 
  string parameter.
  
  `GET /status/`
  
  Retrieves the state of *all* tasks. Similarly to the previous endpoint, the output is also paginated, and the same 
  parameters are used for subsequent requests and filtering out past entries.


#### Common query string parameters
* The following parameters can be added to the query string of any endpoint:

  `pretty` or `pretty=true` &mdash; return indented, human readable JSON in the HTTP response 


### Design considerations


### Installing and running
