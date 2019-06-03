#!/bin/sh

curl -X POST \
  http://localhost:8080/v1/push \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  -d '{
    "uids": [
        "molon"
    ],
    "platform_config": {
        "platforms": [
            "desktop",
            "mobile"
        ],
        "without_platforms": []
    },
    "exclusive_platform_config": {
        "uid1": {
            "platforms": [],
            "without_platforms": [
                "desktop"
            ]
        }
    },
    "msg_bodies": [
    {
       "@type": "type.googleapis.com/google.protobuf.StringValue",
       "value": "offline"
     },
     {
       "@type": "type.googleapis.com/google.protobuf.StringValue",
       "value": "test2"
     }
    ],
    "msg_options": 7,
    "exclusive_msg_options": {
        "uid2": 4
    },
    "reserve": {
       "@type": "type.googleapis.com/google.protobuf.StringValue",
       "value": "reserve content"
     }
}'