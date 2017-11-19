package main

import (
    elastic "gopkg.in/olivere/elastic.v3"
    "fmt"
    "encoding/json"
    "net/http"
    "log"
    "strconv"
    "reflect"
    "context"
    "cloud.google.com/go/bigtable"
    "github.com/pborman/uuid"
)

type Location struct {
    Lat float64 `json:"lat"`
    Lon float64 `json:"lon"`
}

type Post struct {
    // `json:"user"` is for the json parsing of this User field. Otherwise, by default it's 'User'.
    User     string `json:"user"`
    Message  string  `json:"message"`
    Location Location `json:"location"`
}
const (
    INDEX = "around"
    TYPE = "post"
    DISTANCE = "200km"
    // Needs to update
    PROJECT_ID = "geolocation-185822"
    BT_INSTANCE = "geolocation-post"
    // Needs to update this URL if you deploy it to cloud.
    ES_URL = "http://35.196.1.194:9200"
)



func main() {
    // Create a client
    client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
    if err != nil {
        panic(err)
        return
    }

    // Use the IndexExists service to check if a specified index exists.
    exists, err := client.IndexExists(INDEX).Do()
    if err != nil {
        panic(err)
    }
    if !exists {
        // Create a new index.
        mapping := `{
                    "mappings":{
                           "post":{
                                  "properties":{
                                         "location":{
                                                "type":"geo_point"
                                         }
                                  }
                           }
                    }
             }
             `
        _, err := client.CreateIndex(INDEX).Body(mapping).Do()
        if err != nil {
            // Handle error
            panic(err)
        }
    }

    fmt.Println("started-service")
    http.HandleFunc("/post", handlerPost)
    http.HandleFunc("/search", handlerSearch)

    log.Fatal(http.ListenAndServe(":8080", nil))

}


func handlerSearch(w http.ResponseWriter, r *http.Request) {
    // Parse from body of request to get a json object.
    fmt.Println("Received one search request")
    // http://localhost:8080/search?lat=10.0&lon=20.0
    lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
    lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
    // range is optional
    ran := DISTANCE
    if val := r.URL.Query().Get("range"); val != "" {
        ran = val + "km"
    }

    fmt.Printf( "Search received: %f %f %s\n", lat, lon, ran)

    // Create a client
    client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
    if err != nil {
        panic(err)
        return
    }

    // Define geo distance query as specified in
    // https://www.elastic.co/guide/en/elasticsearch/reference/5.2/query-dsl-geo-distance-query.html
    q := elastic.NewGeoDistanceQuery("location")
    q = q.Distance(ran).Lat(lat).Lon(lon)

    // Some delay may range from seconds to minutes. So if you don't get enough results. Try it later.
    searchResult, err := client.Search().
            Index(INDEX).
            Query(q).
            Pretty(true).
            Do()
    if err != nil {
        // Handle error
        panic(err)
    }

    // searchResult is of type SearchResult and returns hits, suggestions,
    // and all kinds of other information from Elasticsearch.
    fmt.Printf("Query took %d milliseconds\n", searchResult.TookInMillis)
    // TotalHits is another convenience function that works even when something goes wrong.
    fmt.Printf("Found a total of %d post\n", searchResult.TotalHits())

    // Each is a convenience function that iterates over hits in a search result.
    // It makes sure you don't need to check for nil values in the response.
    // However, it ignores errors in serialization.
    var typ Post
    var ps []Post
    for _, item := range searchResult.Each(reflect.TypeOf(typ)) {
        p := item.(Post) // p = (Post) item
        fmt.Printf("Post by %s: %s at lat %v and lon %v\n", p.User, p.Message, p.Location.Lat, p.Location.Lon)
        ps = append(ps, p)

    }
    js, err := json.Marshal(ps)
    if err != nil {
        panic(err)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Write(js)


}

func handlerPost(w http.ResponseWriter, r *http.Request) {
    // Parse from body of request to get a json object.
    fmt.Println("Received one post request")
    decoder := json.NewDecoder(r.Body)
    var p Post
    if err := decoder.Decode(&p); err != nil {
        panic(err)
        return
    }

    fmt.Fprintf(w, "Post received: %s\n", p.Message)

    id := uuid.New()
    // Save to ES.
    saveToES(&p, id)

    ctx := context.Background()
    // you must update project name here
    bt_client, err := bigtable.NewClient(ctx, PROJECT_ID, BT_INSTANCE)
    if err != nil {
        panic(err)
        return
    }
    tbl := bt_client.Open("post")
    mut := bigtable.NewMutation()
    t := bigtable.Now()

    mut.Set("post", "user", t, []byte(p.User))
    mut.Set("post", "message", t, []byte(p.Message))
    mut.Set("location", "lat", t, []byte(strconv.FormatFloat(p.Location.Lat, 'f', -1, 64)))
    mut.Set("location", "lon", t, []byte(strconv.FormatFloat(p.Location.Lon, 'f', -1, 64)))

    err = tbl.Apply(ctx, id, mut)
    if err != nil {
        panic(err)
        return
    }
    fmt.Printf("Post is saved to BigTable: %s\n", p.Message)

}

// Save a post to ElasticSearch
func saveToES(p *Post, id string) {
    // Create a client
    es_client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
    if err != nil {
        panic(err)
        return
    }

    // Save it to index
    _, err = es_client.Index().
            Index(INDEX).
            Type(TYPE).
            Id(id).
            BodyJson(p).
            Refresh(true).
            Do()
    if err != nil {
        panic(err)
        return
    }

    fmt.Printf("Post is saved to Index: %s\n", p.Message)
}

