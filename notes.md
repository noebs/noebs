# Notes on go
- First write the HTTP Client library for EBS.
- Fields validation issue.
- The good thing in w http.ResponseWriter is that i can, at anytime, just raise 400s! I mean, WOW!
- router handler, such that you'd transfer your request into it.
- multiple response.WriteHeader calls. I totally got that. Now fixing.
- store the incoming request into a EndpointFields handler so you can decode/encode it multiple times. Good 
in case you need to do logging/further validation with the request body.
- parse EBS response body since they don't use proper HTTP codes. You have to parse that off the response body. I will
a slice for that. The good thing is, EBS response is not nested, so I will only search for `responseCode == 0` in the
request body and just either return 200 (if true), or 400 if wrong.
- Horray! My idea, about this project is to experiment with writing microservices in go; both of which I'm learning. Turns out,
the exact idea of building layers on top of each other's for every service is what gokit is doing. Which is really cool!
- properly use environmental variables to handle e.g., EBS url and other parameters
- i don't like the amount of DRY i'm violating here.
    - I already have skeleton handler for sending to EBS.
    - Now, i need to make another skeleton to handle the rest of

- what does our endpoint handler do?
- check for the request fields
- pass them over onto the next layer, in this case to EBS

## Notes on the current implementation
Well, it is really simple and I shouldn't be worried about it that much. But...

- I can chain gin handlers, and using c.Next(), I can give the control onto the other one
- each handler accepts only gin.Contect object, which captures everything about the request
- the request body is a stream. It is consumed *only* once and then it gets emptied.
- the request body is `Unmarshalled` into a request field template, one for each endpoint
- and then, if succeeded, will be passed onto EBS Client
- EBSClient accepts (gin.Context, url), for:
    - gin.Context to return http responses
    - url to call the appropriate EBS endpoint

** Fuck all of that, or the last section of it. I'm smart ass!