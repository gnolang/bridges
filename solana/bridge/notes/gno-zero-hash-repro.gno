// PKGPATH: gno.land/r/demo/parentfirst
package parentfirst

type Child struct{ X int }

type Parent struct {
	Counter int
	Kid     Child
}

var p Parent

func main() {
	p.Counter++ // the Parent StructValue is marked dirty first  (rlm.updated[0])
	p.Kid.X = 1 // the Child StructValue is marked dirty second (rlm.updated[1])
}

// Realm:
// finalizerealm["gno.land/r/demo/parentfirst"]
// u[0c7fbdfc812fb0dc64b9b8864caa198213b88c69:4](5)=
//     @@ -1,6 +1,7 @@
//      {
//          "Fields": [
//              {
//     +            "N": "AQAAAAAAAAA=",
//                  "T": {
//                      "@type": "/gno.PrimitiveType",
//                      "value": "32"
//     @@ -13,7 +14,6 @@
//                  },
//                  "V": {
//                      "@type": "/gno.RefValue",
//     -                "Hash": "153c64d50a6d3e3799f8667a38498b2805754c9b",
//                      "ObjectID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:5"
//                  }
//              }
//     @@ -21,7 +21,7 @@
//          "ObjectInfo": {
//              "ID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:4",
//              "LastObjectSize": "385",
//     -        "ModTime": "0",
//     +        "ModTime": "7",
//              "OwnerID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:3",
//              "RefCount": "1"
//          }
// u[0c7fbdfc812fb0dc64b9b8864caa198213b88c69:5](5)=
//     @@ -1,6 +1,7 @@
//      {
//          "Fields": [
//              {
//     +            "N": "AQAAAAAAAAA=",
//                  "T": {
//                      "@type": "/gno.PrimitiveType",
//                      "value": "32"
//     @@ -10,7 +11,7 @@
//          "ObjectInfo": {
//              "ID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:5",
//              "LastObjectSize": "214",
//     -        "ModTime": "0",
//     +        "ModTime": "7",
//              "OwnerID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:4",
//              "RefCount": "1"
//          }
// u[0c7fbdfc812fb0dc64b9b8864caa198213b88c69:3](5)=
//     @@ -2,7 +2,7 @@
//          "ObjectInfo": {
//              "ID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:3",
//              "LastObjectSize": "350",
//     -        "ModTime": "0",
//     +        "ModTime": "7",
//              "OwnerID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:2",
//              "RefCount": "1"
//          },
//     @@ -13,7 +13,7 @@
//              },
//              "V": {
//                  "@type": "/gno.RefValue",
//     -            "Hash": "9e40cf0385ba93544ee6037ab5f52e28441a0cca",
//     +            "Hash": "c1f83bbba032e0e2ecc0b1d0d3af7448ace155c6",
//                  "ObjectID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:4"
//              }
//          }
// u[0c7fbdfc812fb0dc64b9b8864caa198213b88c69:2](5)=
//     @@ -4,7 +4,7 @@
//              "ID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:2",
//              "IsEscaped": true,
//              "LastObjectSize": "712",
//     -        "ModTime": "0",
//     +        "ModTime": "7",
//              "RefCount": "2"
//          },
//          "Parent": null,
//     @@ -58,7 +58,7 @@
//                  },
//                  "V": {
//                      "@type": "/gno.RefValue",
//     -                "Hash": "9e8bd56c6e00eeedff3b1d70d72eb355d8c49373",
//     +                "Hash": "08e2f92c73aad718fedf4eebd2f09dc51740e270",
//                      "ObjectID": "0c7fbdfc812fb0dc64b9b8864caa198213b88c69:3"
//                  }
//              },
